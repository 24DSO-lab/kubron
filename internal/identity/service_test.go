// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// ---------- фикстуры ----------

var (
	adminA   = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-a"} // Team Admin команды team-a
	adminB   = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-b"} // Team Admin команды team-b
	orgAdmin = identity.ActorRef{Kind: identity.ActorUser, ID: "org-admin"}
	ctx      = context.Background()
)

type fakeRec struct {
	mu          sync.Mutex
	events      *[]string
	intents     map[string]audit.CriticalIntent
	outcomes    map[string]audit.CriticalOutcome
	order       []string
	failIntent  error
	failOutcome error
	getErr      error
}

func newRec(events *[]string) *fakeRec {
	return &fakeRec{events: events, intents: map[string]audit.CriticalIntent{}, outcomes: map[string]audit.CriticalOutcome{}}
}

func (r *fakeRec) log(s string) {
	if r.events != nil {
		*r.events = append(*r.events, s)
	}
}

func (r *fakeRec) AppendIntent(c context.Context, in audit.CriticalIntent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failIntent != nil {
		return r.failIntent
	}
	if err := in.Validate(); err != nil { // реальная валидация audit: наши intent'ы обязаны быть валидны
		return err
	}
	if old, ok := r.intents[in.EventID]; ok {
		if old != in {
			return audit.ErrCriticalConflict
		}
		return nil
	}
	r.intents[in.EventID] = in
	r.order = append(r.order, in.EventID)
	r.log("intent:" + in.Action)
	return nil
}

func (r *fakeRec) AppendOutcome(c context.Context, out audit.CriticalOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := c.Err(); err != nil { // исход обязан писаться даже после отмены ctx вызывающего
		return err
	}
	if r.failOutcome != nil {
		return r.failOutcome
	}
	if err := out.Validate(); err != nil {
		return err
	}
	if _, ok := r.intents[out.EventID]; !ok {
		return audit.ErrCriticalNotFound
	}
	if old, ok := r.outcomes[out.EventID]; ok {
		if old != out {
			return audit.ErrCriticalConflict
		}
		return nil
	}
	r.outcomes[out.EventID] = out
	r.log("outcome:" + string(out.Result))
	return nil
}

func (r *fakeRec) GetOutcome(c context.Context, orgID, eventID string) (audit.CriticalOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return audit.CriticalOutcome{}, r.getErr
	}
	in, ok := r.intents[eventID]
	if !ok || in.OrgID != orgID {
		return audit.CriticalOutcome{}, audit.ErrCriticalNotFound
	}
	out, ok := r.outcomes[eventID]
	if !ok {
		return audit.CriticalOutcome{}, audit.ErrCriticalNotFound
	}
	return out, nil
}

func (r *fakeRec) ListPending(c context.Context, orgID string, limit int) ([]audit.CriticalIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []audit.CriticalIntent
	for _, id := range r.order {
		if _, done := r.outcomes[id]; !done && r.intents[id].OrgID == orgID {
			out = append(out, r.intents[id])
		}
	}
	return out, nil
}

// intentsFor возвращает intent'ы по действию.
func (r *fakeRec) byAction(action string) []audit.CriticalIntent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []audit.CriticalIntent
	for _, id := range r.order {
		if r.intents[id].Action == action {
			out = append(out, r.intents[id])
		}
	}
	return out
}

func (r *fakeRec) count() (intents, outcomes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.intents), len(r.outcomes)
}

// spyBackend считает мутации и позволяет подставить сбой/хук.
type spyBackend struct {
	identity.Backend
	mu        sync.Mutex
	events    *[]string
	mutations int
	failNext  error
	after     func()
}

func (s *spyBackend) mutate(name string, fn func() error) error {
	s.mu.Lock()
	s.mutations++
	if s.events != nil {
		*s.events = append(*s.events, "backend:"+name)
	}
	fail := s.failNext
	s.failNext = nil
	after := s.after
	s.mu.Unlock()
	if fail != nil {
		return fail
	}
	err := fn()
	if after != nil {
		after()
	}
	return err
}

func (s *spyBackend) Create(c context.Context, in identity.Identity) (out identity.Identity, err error) {
	err = s.mutate("create", func() (e error) { out, e = s.Backend.Create(c, in); return })
	return
}

func (s *spyBackend) SetState(c context.Context, r identity.PrincipalRef, rev uint64, n identity.IdentityState, m, e string) (out identity.Identity, err error) {
	err = s.mutate("setstate", func() (x error) { out, x = s.Backend.SetState(c, r, rev, n, m, e); return })
	return
}

func (s *spyBackend) PutBinding(c context.Context, in identity.CredentialBinding, rev uint64, m, e string) (out identity.CredentialBinding, err error) {
	err = s.mutate("putbinding", func() (x error) { out, x = s.Backend.PutBinding(c, in, rev, m, e); return })
	return
}

func (s *spyBackend) ActivateBinding(c context.Context, id string, rev uint64, m, e string) (out identity.CredentialBinding, err error) {
	err = s.mutate("activate", func() (x error) { out, x = s.Backend.ActivateBinding(c, id, rev, m, e); return })
	return
}

func (s *spyBackend) RevokeBinding(c context.Context, id string, rev uint64, m, e string) (out identity.CredentialBinding, err error) {
	err = s.mutate("revokebinding", func() (x error) { out, x = s.Backend.RevokeBinding(c, id, rev, m, e); return })
	return
}

func (s *spyBackend) UpdateDisplayName(c context.Context, r identity.PrincipalRef, rev uint64, name, m, e string) (out identity.Identity, err error) {
	err = s.mutate("update_display_name", func() (x error) { out, x = s.Backend.UpdateDisplayName(c, r, rev, name, m, e); return })
	return
}

func (s *spyBackend) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.mutations }

// fakeAuthz — модель app-RBAC: Team Admin управляет Team identity только своей команды, Organization Admin —
// Service identity только своей организации. Роль от клиента не принимается: только actor.ID + владелец.
type fakeAuthz struct {
	mu     sync.Mutex
	grants map[string][]identity.OwnerScope // actor ID → области
	err    error                            // принудительная ошибка зависимости
	calls  []authzCall
}

type authzCall struct {
	actor  identity.ActorRef
	action identity.AdminAction
	owner  identity.OwnerScope
}

func (a *fakeAuthz) AuthorizeIdentityAdmin(c context.Context, actor identity.ActorRef, action identity.AdminAction, owner identity.OwnerScope) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, authzCall{actor, action, owner})
	if a.err != nil {
		return a.err
	}
	for _, o := range a.grants[actor.ID] {
		if o == owner {
			return nil
		}
	}
	return identity.ErrForbidden
}

func (a *fakeAuthz) revoke(actorID string) { a.mu.Lock(); delete(a.grants, actorID); a.mu.Unlock() }
func (a *fakeAuthz) callCount() int        { a.mu.Lock(); defer a.mu.Unlock(); return len(a.calls) }

type fakeClusters struct {
	owners map[string]identity.OwnerScope
	err    error
}

func (f *fakeClusters) ResolveClusterOwner(c context.Context, id string) (identity.OwnerScope, error) {
	if f.err != nil {
		return identity.OwnerScope{}, f.err
	}
	o, ok := f.owners[id]
	if !ok {
		return identity.OwnerScope{}, identity.ErrNotFound
	}
	return o, nil
}

var (
	teamAScope = identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: "org-a", TeamID: "team-a"}
	teamBScope = identity.OwnerScope{Kind: identity.OwnerTeam, OrgID: "org-a", TeamID: "team-b"}
	orgAScope  = identity.OwnerScope{Kind: identity.OwnerOrganization, OrgID: "org-a"}
)

type env struct {
	mu       sync.Mutex
	t        *testing.T
	svc      *identity.Service
	be       *spyBackend
	file     *identity.FileBackend
	dir      string
	rec      *fakeRec
	authz    *fakeAuthz
	clusters *fakeClusters
	events   []string
	now      time.Time
	seq      int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, dir: t.TempDir(), now: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	fb, err := identity.OpenFile(e.dir, identity.WithFileClock(func() time.Time {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.now
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.file = fb
	e.be = &spyBackend{Backend: fb, events: &e.events}
	e.rec = newRec(&e.events)
	e.authz = &fakeAuthz{grants: map[string][]identity.OwnerScope{
		"admin-a": {teamAScope}, "admin-b": {teamBScope}, "org-admin": {orgAScope},
	}}
	e.clusters = &fakeClusters{owners: map[string]identity.OwnerScope{
		"cl-a": teamAScope, "cl-b": teamBScope,
		"cl-other-org": {Kind: identity.OwnerTeam, OrgID: "org-x", TeamID: "team-x"},
	}}
	e.svc, err = identity.NewService(e.be, e.rec, e.authz, e.clusters,
		identity.WithClock(func() time.Time {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.now = e.now.Add(time.Second)
			return e.now
		}),
		identity.WithEventIDs(func() (string, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.seq++
			return fmt.Sprintf("evt-%04d", e.seq), nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) mut() string { e.seq++; return fmt.Sprintf("mut-%04d", e.seq) }

func (e *env) createTeam(mutID string) identity.Identity {
	e.t.Helper()
	got, err := e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "deploy-bot", MutationID: mutID})
	if err != nil {
		e.t.Fatalf("create team identity: %v", err)
	}
	return got
}

func (e *env) createService(mutID string) identity.Identity {
	e.t.Helper()
	got, err := e.svc.Create(ctx, identity.CreateRequest{Actor: orgAdmin, Kind: identity.PrincipalService, OrgID: "org-a", DisplayName: "nightly", MutationID: mutID})
	if err != nil {
		e.t.Fatalf("create service identity: %v", err)
	}
	return got
}

func is(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: want %v, got %v", what, want, err)
	}
}

func isNot(t *testing.T, what string, err, bad error) {
	t.Helper()
	if errors.Is(err, bad) {
		t.Errorf("%s: unexpected %v", what, bad)
	}
}

// ---------- конструктор ----------

func TestNewService_RejectsMissingDependencies(t *testing.T) {
	e := newEnv(t)
	cases := map[string]func() (*identity.Service, error){
		"backend":    func() (*identity.Service, error) { return identity.NewService(nil, e.rec, e.authz, e.clusters) },
		"recorder":   func() (*identity.Service, error) { return identity.NewService(e.be, nil, e.authz, e.clusters) },
		"authorizer": func() (*identity.Service, error) { return identity.NewService(e.be, e.rec, nil, e.clusters) },
		"clusters":   func() (*identity.Service, error) { return identity.NewService(e.be, e.rec, e.authz, nil) },
	}
	for name, f := range cases {
		if s, err := f(); s != nil || !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

// ---------- создание и протокол аудита ----------

func TestService_Create_ProtocolOrderAndAuditContent(t *testing.T) {
	e := newEnv(t)
	got := e.createTeam("req-1")

	// §7.2: intent → ровно одна мутация Backend → outcome succeeded.
	want := []string{"intent:identity.create", "backend:create", "outcome:succeeded"}
	if strings.Join(e.events, ",") != strings.Join(want, ",") {
		t.Fatalf("protocol order: %v", e.events)
	}
	if got.Revision != 1 || got.State != identity.IdentityActive || got.CreatedBy != adminA ||
		got.Owner != teamAScope || got.Ref.Kind != identity.PrincipalTeam || got.Ref.TeamID != "team-a" ||
		!strings.HasPrefix(got.Ref.ID, "tid-") || got.LastMutationID != "req-1" || got.DisplayName != "deploy-bot" {
		t.Fatalf("identity: %+v", got)
	}
	in := e.rec.byAction("identity.create")[0]
	if in.EventID != got.LastAuditEventID || in.MutationID != "req-1" || in.ResourceType != "identity" || in.ResourceID != got.Ref.ID ||
		in.OrgID != "org-a" || in.TeamID != "team-a" || in.PrincipalKind != "team" || in.PrincipalID != got.Ref.ID ||
		in.ActorKind != "user" || in.ActorID != "admin-a" || !strings.HasPrefix(in.RequestDigest, "sha256:") || in.ExpectedRevision != 0 {
		t.Fatalf("intent: %+v", in)
	}
	out, err := e.rec.GetOutcome(ctx, "org-a", got.LastAuditEventID)
	if err != nil || out.Result != audit.CriticalSucceeded || out.ResourceRevision != 1 {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	// В аудит не попадают display name и иные свободные тексты.
	raw, _ := json.Marshal(in)
	if strings.Contains(string(raw), "deploy-bot") {
		t.Fatalf("display name leaked into audit: %s", raw)
	}
	if res, err := e.svc.Resolve(ctx, got.Ref); err != nil || res.Ref != got.Ref {
		t.Fatalf("resolve after confirmed audit: %+v %v", res, err)
	}
}

func TestService_Create_ServiceIdentityIsOrganizationOwned(t *testing.T) {
	e := newEnv(t)
	got := e.createService("req-1")
	if got.Owner != orgAScope || got.Ref.Kind != identity.PrincipalService || got.Ref.TeamID != "" || !strings.HasPrefix(got.Ref.ID, "sid-") {
		t.Fatalf("service identity: %+v", got)
	}
	if call := e.authz.calls[len(e.authz.calls)-1]; call.owner != orgAScope || call.action != identity.AdminCreate {
		t.Fatalf("authorizer must see the trusted owner derived from the ref: %+v", call)
	}
	if in := e.rec.byAction("identity.create")[0]; in.TeamID != "" || in.PrincipalKind != "service" {
		t.Fatalf("intent: %+v", in)
	}
}

func TestService_Create_ValidationHappensBeforeAnySideEffect(t *testing.T) {
	e := newEnv(t)
	base := identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "x", MutationID: "req-1"}
	mut := map[string]func(*identity.CreateRequest){
		"user kind":         func(r *identity.CreateRequest) { r.Kind = identity.PrincipalUser },
		"unknown kind":      func(r *identity.CreateRequest) { r.Kind = "robot" },
		"team w/o team":     func(r *identity.CreateRequest) { r.TeamID = "" },
		"team w/o org":      func(r *identity.CreateRequest) { r.OrgID = "" },
		"bad org":           func(r *identity.CreateRequest) { r.OrgID = "a b" },
		"empty name":        func(r *identity.CreateRequest) { r.DisplayName = "   " },
		"control in name":   func(r *identity.CreateRequest) { r.DisplayName = "a\nb" },
		"empty mutation id": func(r *identity.CreateRequest) { r.MutationID = "" },
		"bad mutation id":   func(r *identity.CreateRequest) { r.MutationID = "a/b" },
	}
	for name, m := range mut {
		r := base
		m(&r)
		if _, err := e.svc.Create(ctx, r); !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	svc := base
	svc.Kind, svc.TeamID = identity.PrincipalService, "team-a" // service с TeamID
	if _, err := e.svc.Create(ctx, svc); !errors.Is(err, identity.ErrInvalid) {
		t.Errorf("service with team id: %v", err)
	}
	if n, _ := e.rec.count(); n != 0 || e.be.count() != 0 || e.authz.callCount() != 0 {
		t.Fatalf("invalid requests must have no side effects: intents=%d mutations=%d authz=%d", n, e.be.count(), e.authz.callCount())
	}
}

func TestService_Create_IdempotentReplayAndConflict(t *testing.T) {
	e := newEnv(t)
	first := e.createTeam("req-1")
	intents, _ := e.rec.count()

	again, err := e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "deploy-bot", MutationID: "req-1"})
	if err != nil || again.Ref != first.Ref || again.Revision != 1 || again.LastAuditEventID != first.LastAuditEventID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if n, _ := e.rec.count(); n != intents || e.be.count() != 1 {
		t.Fatalf("replay must not add intents/mutations: intents %d→%d mutations=%d", intents, n, e.be.count())
	}
	// Тот же ключ, другое содержимое — конфликт, а не тихая подмена.
	_, err = e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "other", MutationID: "req-1"})
	is(t, "same key, other payload", err, identity.ErrConflict)
	// Новый ключ — новая независимая identity (ID не совпадают).
	second := e.createTeam("req-2")
	if second.Ref.ID == first.Ref.ID {
		t.Fatal("distinct mutation ids must yield distinct principal ids")
	}
	// Тот же ключ в другой команде/организации не пересекается.
	other, err := e.svc.Create(ctx, identity.CreateRequest{Actor: adminB, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-b", DisplayName: "deploy-bot", MutationID: "req-1"})
	if err != nil || other.Ref.ID == first.Ref.ID {
		t.Fatalf("same key in another team: %+v %v", other, err)
	}
}

func TestService_Create_ConcurrentIdenticalRequestsConverge(t *testing.T) {
	e := newEnv(t)
	// This test exercises concurrent service calls; event ordering is covered separately.
	// Disable the shared, intentionally non-concurrent event trace to avoid racing
	// between fakeRec and spyBackend while they append to the same slice.
	e.rec.events = nil
	e.be.events = nil

	const n = 16
	var wg sync.WaitGroup
	res := make([]identity.Identity, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], errs[i] = e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "deploy-bot", MutationID: "req-1"})
		}(i)
	}
	wg.Wait()
	for i := range res {
		if errs[i] != nil || res[i].Ref != res[0].Ref {
			t.Fatalf("call %d: %+v %v", i, res[i], errs[i])
		}
	}
	list, _ := e.file.List(ctx, teamAScope)
	if len(list) != 1 || list[0].Revision != 1 {
		t.Fatalf("exactly one identity expected: %+v", list)
	}
	if _, err := e.svc.Resolve(ctx, list[0].Ref); err != nil {
		t.Fatalf("converged identity must be usable: %v", err)
	}
}

// ---------- полномочия ----------

func TestService_Authorization_FailClosedWithoutSideEffects(t *testing.T) {
	req := func(a identity.ActorRef) identity.CreateRequest {
		return identity.CreateRequest{Actor: a, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "x", MutationID: "req-1"}
	}
	t.Run("denied", func(t *testing.T) {
		e := newEnv(t)
		_, err := e.svc.Create(ctx, req(identity.ActorRef{Kind: identity.ActorUser, ID: "stranger"}))
		is(t, "stranger", err, identity.ErrForbidden)
		if n, _ := e.rec.count(); n != 0 || e.be.count() != 0 {
			t.Fatalf("denied request left traces: intents=%d mutations=%d", n, e.be.count())
		}
	})
	t.Run("other team admin has no rights", func(t *testing.T) {
		e := newEnv(t)
		_, err := e.svc.Create(ctx, req(adminB))
		is(t, "team-b admin creating in team-a", err, identity.ErrForbidden)
		svcReq := identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalService, OrgID: "org-a", DisplayName: "x", MutationID: "req-2"}
		_, err = e.svc.Create(ctx, svcReq)
		is(t, "team admin creating organization service identity", err, identity.ErrForbidden)
		teamReq := req(orgAdmin)
		_, err = e.svc.Create(ctx, teamReq)
		is(t, "org admin has no implicit team-identity rights", err, identity.ErrForbidden)
	})
	t.Run("only a human user may administer", func(t *testing.T) {
		e := newEnv(t)
		for _, k := range []identity.ActorKind{identity.ActorSystem, identity.ActorScheduler} {
			_, err := e.svc.Create(ctx, req(identity.ActorRef{Kind: k, ID: "admin-a"}))
			is(t, string(k), err, identity.ErrForbidden)
		}
		if e.authz.callCount() != 0 {
			t.Fatal("non-user actors must be refused before consulting the authorizer")
		}
	})
	t.Run("invalid actor", func(t *testing.T) {
		e := newEnv(t)
		for _, a := range []identity.ActorRef{{}, {Kind: identity.ActorUser}, {Kind: "robot", ID: "x"}, {Kind: identity.ActorUser, ID: "a b"}} {
			_, err := e.svc.Create(ctx, req(a))
			is(t, fmt.Sprintf("%+v", a), err, identity.ErrInvalid)
		}
	})
	t.Run("authorizer unavailable", func(t *testing.T) {
		e := newEnv(t)
		for _, ae := range []error{identity.ErrUnavailable, errors.New("rbac backend down")} {
			e.authz.err = ae
			_, err := e.svc.Create(ctx, req(adminA))
			is(t, "authorizer error", err, identity.ErrUnavailable)
			isNot(t, "must not look like an allow or a clean deny", err, identity.ErrForbidden)
		}
		if n, _ := e.rec.count(); n != 0 || e.be.count() != 0 {
			t.Fatal("unavailable authorizer left traces")
		}
	})
	t.Run("an identity principal is not an administrator", func(t *testing.T) {
		e := newEnv(t)
		created := e.createTeam("req-1")
		// Попытка выдать идентификатор технической identity за actor'а: прав у неё нет — и не может быть.
		_, err := e.svc.Suspend(ctx, identity.TransitionRequest{
			Actor: identity.ActorRef{Kind: identity.ActorUser, ID: created.Ref.ID}, Ref: created.Ref, ExpectedRevision: 1, MutationID: e.mut()})
		is(t, "identity acting on itself", err, identity.ErrForbidden)
		if got, _ := e.file.Get(ctx, created.Ref); got.State != identity.IdentityActive {
			t.Fatal("state changed")
		}
	})
}

func TestService_UpdateDisplayName_UsesAuditedCASAndPreservesImmutableFields(t *testing.T) {
	e := newEnv(t)
	created := e.createTeam("req-1")
	e.events = nil // isolate the update's intent → mutation → outcome ordering
	got, err := e.svc.UpdateDisplayName(ctx, adminA, created.Ref, created.Revision, "  renamed-bot  ", "rename-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "renamed-bot" || got.Revision != created.Revision+1 ||
		got.Ref != created.Ref || got.Owner != created.Owner || got.State != created.State ||
		got.CreatedBy != created.CreatedBy || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("unexpected updated identity: before=%+v after=%+v", created, got)
	}
	if strings.Join(e.events, ",") != "intent:identity.display_name.update,backend:update_display_name,outcome:succeeded" {
		t.Fatalf("audit/mutation order: %v", e.events)
	}
	in := e.rec.byAction("identity.display_name.update")[0]
	raw, _ := json.Marshal(in)
	if strings.Contains(string(raw), "renamed-bot") {
		t.Fatalf("display name leaked into audit: %s", raw)
	}
	mutations := e.be.count()
	again, err := e.svc.UpdateDisplayName(ctx, adminA, created.Ref, created.Revision, "renamed-bot", "rename-1")
	if err != nil || again.Revision != got.Revision || again.LastAuditEventID != got.LastAuditEventID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if e.be.count() != mutations {
		t.Fatal("replay repeated backend mutation")
	}
	_, err = e.svc.UpdateDisplayName(ctx, adminA, created.Ref, created.Revision, "other-name", "rename-1")
	is(t, "same mutation with different display name", err, identity.ErrConflict)
}

func TestService_CreatorDepartureChangesNothing(t *testing.T) {
	e := newEnv(t)
	created := e.createTeam("req-1")
	e.authz.revoke("admin-a") // создатель ушёл / потерял роль

	// Identity жива, владелец и provenance не изменились, статус не поменялся.
	got, err := e.svc.Resolve(ctx, created.Ref)
	if err != nil || got.State != identity.IdentityActive || got.Owner != teamAScope || got.CreatedBy != adminA {
		t.Fatalf("identity after creator departure: %+v %v", got, err)
	}
	// Создатель больше не может ею управлять: создание не даёт authority.
	_, err = e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: created.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "former creator", err, identity.ErrForbidden)
	// Текущий администратор той же команды — может, и владелец по-прежнему команда, а не он.
	e.authz.grants["new-admin"] = []identity.OwnerScope{teamAScope}
	s, err := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: identity.ActorRef{Kind: identity.ActorUser, ID: "new-admin"}, Ref: created.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	if err != nil || s.Owner != teamAScope || s.CreatedBy != adminA {
		t.Fatalf("suspend by current admin: %+v %v", s, err)
	}
}

// ---------- сбои аудита ----------

func TestService_IntentFailureBlocksEveryMutation(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	svc := e.createService("req-2")
	bd, err := e.svc.AssignBinding(ctx, identity.AssignBindingRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", MutationID: e.mut()})
	if err != nil {
		t.Fatal(err)
	}
	before := e.be.count()

	e.rec.failIntent = errors.New("journal disk full")
	calls := map[string]func() error{
		"create": func() error {
			_, err := e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "x", MutationID: "req-9"})
			return err
		},
		"suspend": func() error {
			_, err := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
			return err
		},
		"revoke": func() error {
			_, err := e.svc.Revoke(ctx, identity.TransitionRequest{Actor: orgAdmin, Ref: svc.Ref, ExpectedRevision: 1, MutationID: e.mut()})
			return err
		},
		"assign binding": func() error {
			_, err := e.svc.AssignBinding(ctx, identity.AssignBindingRequest{Actor: orgAdmin, Principal: svc.Ref, ClusterID: "cl-a", MutationID: e.mut()})
			return err
		},
		"activate binding": func() error {
			_, err := e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: bd.Revision, MutationID: e.mut()})
			return err
		},
		"revoke binding": func() error {
			_, err := e.svc.RevokeBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: bd.Revision, MutationID: e.mut()})
			return err
		},
	}
	for name, call := range calls {
		err := call()
		is(t, name, err, identity.ErrUnavailable)
		isNot(t, name+" must not report success or unknown outcome", err, audit.ErrOutcomeUnknown)
	}
	if e.be.count() != before {
		t.Fatalf("Backend was called despite intent failure: %d → %d", before, e.be.count())
	}
	if got, _ := e.file.Get(ctx, team.Ref); got.Revision != 1 || got.State != identity.IdentityActive {
		t.Fatalf("state changed without durable intent: %+v", got)
	}
	if b, _ := e.file.GetBinding(ctx, team.Ref, "cl-a"); b.State != identity.BindingPending || b.Revision != 1 {
		t.Fatalf("binding changed without durable intent: %+v", b)
	}
}

func TestService_OutcomeFailure_IsUnknown_BlocksUse_AndReconcilesWithoutRemutation(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		e := newEnv(t)
		e.rec.failOutcome = errors.New("journal write failed")
		req := identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "bot", MutationID: "req-1"}
		_, err := e.svc.Create(ctx, req)
		is(t, "create", err, audit.ErrOutcomeUnknown)
		isNot(t, "must not claim success", err, identity.ErrConflict)

		list, _ := e.file.List(ctx, teamAScope)
		if len(list) != 1 {
			t.Fatalf("mutation was applied, one record expected: %d", len(list))
		}
		// Запись существует, но не пригодна: исход неизвестен.
		_, err = e.svc.Resolve(ctx, list[0].Ref)
		is(t, "resolve with unknown outcome", err, identity.ErrForbidden)

		// Повтор после восстановления журнала: НЕ мутирует заново, а доказательно завершает outcome.
		e.rec.failOutcome = nil
		mutations := e.be.count()
		got, err := e.svc.Create(ctx, req)
		if err != nil || got.Ref != list[0].Ref || got.Revision != 1 {
			t.Fatalf("reconcile: %+v %v", got, err)
		}
		if e.be.count() != mutations {
			t.Fatal("reconciliation must not repeat the mutation")
		}
		if _, err := e.svc.Resolve(ctx, got.Ref); err != nil {
			t.Fatalf("usable after reconciliation: %v", err)
		}
	})
	t.Run("suspend", func(t *testing.T) {
		e := newEnv(t)
		team := e.createTeam("req-1")
		e.rec.failOutcome = errors.New("journal write failed")
		mutID := e.mut()
		tr := identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: mutID}
		_, err := e.svc.Suspend(ctx, tr)
		is(t, "suspend", err, audit.ErrOutcomeUnknown)
		e.rec.failOutcome = nil
		got, err := e.svc.Suspend(ctx, tr)
		if err != nil || got.State != identity.IdentitySuspended || got.Revision != 2 {
			t.Fatalf("reconcile suspend: %+v %v", got, err)
		}
		if cur, _ := e.file.Get(ctx, team.Ref); cur.Revision != 2 {
			t.Fatalf("revision must not move on reconcile: %d", cur.Revision)
		}
	})
	t.Run("a failed-mutation outcome that cannot be written is unknown too", func(t *testing.T) {
		e := newEnv(t)
		team := e.createTeam("req-1")
		e.rec.failOutcome = errors.New("journal write failed")
		_, err := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 99, MutationID: e.mut()})
		is(t, "outcome unknown", err, audit.ErrOutcomeUnknown)
		is(t, "original cause stays inspectable", err, identity.ErrConflict)
	})
	t.Run("outcome is recorded even if the caller canceled after the mutation", func(t *testing.T) {
		e := newEnv(t)
		cctx, cancel := context.WithCancel(ctx)
		e.be.after = cancel
		got, err := e.svc.Create(cctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "bot", MutationID: "req-1"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if out, err := e.rec.GetOutcome(ctx, "org-a", got.LastAuditEventID); err != nil || out.Result != audit.CriticalSucceeded {
			t.Fatalf("outcome must be durable despite cancellation: %+v %v", out, err)
		}
	})
}

func TestService_BackendFailuresAreAuditedAsFailedOrUnknown(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")

	// Определённый отказ (stale revision) → failed/conflict.
	_, err := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 5, MutationID: e.mut()})
	is(t, "stale revision", err, identity.ErrConflict)
	last := e.rec.byAction("identity.suspend")[0]
	out, err := e.rec.GetOutcome(ctx, "org-a", last.EventID)
	if err != nil || out.Result != audit.CriticalFailed || out.ReasonCode != "conflict" {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	// Состояние не менялось и пригодность определяется прежним подтверждённым событием.
	if _, err := e.svc.Resolve(ctx, team.Ref); err != nil {
		t.Fatalf("failed attempt must not poison the identity: %v", err)
	}

	// Недопустимый переход → failed/invalid.
	_, err = e.svc.Resume(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "resume active", err, identity.ErrInvalid)
	if o, _ := e.rec.GetOutcome(ctx, "org-a", e.rec.byAction("identity.resume")[0].EventID); o.ReasonCode != "invalid" {
		t.Fatalf("outcome: %+v", o)
	}

	// Недоступное хранилище → исход неясен → unknown (не failed): мутацию нельзя доказать несостоявшейся.
	e.be.failNext = identity.ErrUnavailable
	_, err = e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "backend unavailable", err, identity.ErrUnavailable)
	ev := e.rec.byAction("identity.suspend")
	if o, _ := e.rec.GetOutcome(ctx, "org-a", ev[len(ev)-1].EventID); o.Result != audit.CriticalUnknown || o.ReasonCode != "backend_unavailable" {
		t.Fatalf("outcome: %+v", o)
	}
	// Неизвестная ошибка зависимости тоже fail closed.
	e.be.failNext = errors.New("weird")
	_, err = e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "unknown backend error", err, identity.ErrUnavailable)
}

func TestService_Resolve_RequiresConfirmedSuccessfulAudit(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	evID := team.LastAuditEventID
	intent := e.rec.intents[evID]

	set := func(o *audit.CriticalOutcome) {
		e.rec.mu.Lock()
		defer e.rec.mu.Unlock()
		delete(e.rec.outcomes, evID)
		if o != nil {
			e.rec.outcomes[evID] = *o
		}
	}
	at := time.Date(2026, 11, 27, 11, 0, 0, 0, time.UTC)
	for name, o := range map[string]*audit.CriticalOutcome{
		"missing": nil,
		"failed":  {EventID: evID, Result: audit.CriticalFailed, ReasonCode: "conflict", CompletedAt: at},
		"unknown": {EventID: evID, Result: audit.CriticalUnknown, ReasonCode: "backend_unavailable", CompletedAt: at},
	} {
		set(o)
		_, err := e.svc.Resolve(ctx, team.Ref)
		is(t, name+" outcome", err, identity.ErrForbidden)
	}
	set(&audit.CriticalOutcome{EventID: evID, Result: audit.CriticalSucceeded, ResourceRevision: 1, CompletedAt: at})
	if _, err := e.svc.Resolve(ctx, team.Ref); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	// Недоступный аудит — не «разрешено» и не «нет такого»: fail closed.
	e.rec.getErr = errors.New("journal requires recovery")
	_, err := e.svc.Resolve(ctx, team.Ref)
	is(t, "audit unavailable", err, identity.ErrUnavailable)
	e.rec.getErr = nil
	// Outcome чужой организации не подтверждает identity: запрос организационно ограничен.
	if _, err := e.rec.GetOutcome(ctx, "org-b", evID); !errors.Is(err, audit.ErrCriticalNotFound) {
		t.Fatalf("foreign org must not see the outcome: %v", err)
	}
	_ = intent
}

// ---------- жизненный цикл ----------

func TestService_Lifecycle(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	tr := func(rev uint64, m string) identity.TransitionRequest {
		return identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: rev, MutationID: m}
	}

	s, err := e.svc.Suspend(ctx, tr(1, "m-suspend"))
	if err != nil || s.State != identity.IdentitySuspended || s.Revision != 2 {
		t.Fatalf("suspend: %+v %v", s, err)
	}
	if got := e.authz.calls[len(e.authz.calls)-1]; got.action != identity.AdminSuspend || got.owner != teamAScope {
		t.Fatalf("authz call: %+v", got)
	}
	_, err = e.svc.Resolve(ctx, team.Ref)
	is(t, "suspended is not usable", err, identity.ErrForbidden)

	// Идемпотентный повтор приостановки не создаёт нового intent и не меняет revision.
	intents, _ := e.rec.count()
	if again, err := e.svc.Suspend(ctx, tr(1, "m-suspend")); err != nil || again.Revision != 2 {
		t.Fatalf("suspend replay: %+v %v", again, err)
	}
	if n, _ := e.rec.count(); n != intents {
		t.Fatal("replay must not append a new intent")
	}
	// Тот же ключ для другого действия — конфликт.
	_, err = e.svc.Revoke(ctx, tr(1, "m-suspend"))
	is(t, "key reused for another transition", err, identity.ErrConflict)

	r, err := e.svc.Resume(ctx, tr(2, "m-resume"))
	if err != nil || r.State != identity.IdentityActive || r.Revision != 3 {
		t.Fatalf("resume: %+v %v", r, err)
	}
	if got := e.authz.calls[len(e.authz.calls)-1]; got.action != identity.AdminUpdate {
		t.Fatalf("resume must be authorized as update: %+v", got)
	}
	if _, err := e.svc.Resolve(ctx, team.Ref); err != nil {
		t.Fatalf("resumed identity must be usable: %v", err)
	}
	// Устаревший писатель.
	_, err = e.svc.Suspend(ctx, tr(2, "m-stale"))
	is(t, "stale revision", err, identity.ErrConflict)
	_, err = e.svc.Suspend(ctx, tr(0, "m-zero"))
	is(t, "zero revision", err, identity.ErrInvalid)

	rv, err := e.svc.Revoke(ctx, tr(3, "m-revoke"))
	if err != nil || rv.State != identity.IdentityRevoked || rv.Revision != 4 {
		t.Fatalf("revoke: %+v %v", rv, err)
	}
	if got := e.authz.calls[len(e.authz.calls)-1]; got.action != identity.AdminRevoke {
		t.Fatalf("authz: %+v", got)
	}
	if again, err := e.svc.Revoke(ctx, tr(3, "m-revoke")); err != nil || again.Revision != 4 {
		t.Fatalf("revoke replay: %+v %v", again, err)
	}
	_, err = e.svc.Resolve(ctx, team.Ref)
	is(t, "revoked", err, identity.ErrRevoked)
	for name, f := range map[string]func() error{
		"resume":  func() error { _, err := e.svc.Resume(ctx, tr(4, "m-r1")); return err },
		"suspend": func() error { _, err := e.svc.Suspend(ctx, tr(4, "m-r2")); return err },
		"revoke":  func() error { _, err := e.svc.Revoke(ctx, tr(4, "m-r3")); return err },
	} {
		is(t, name+" after revoke", f(), identity.ErrRevoked)
	}
	// Revoked читается как метаданные (provenance сохранён), но не пригоден.
	if got, err := e.svc.Get(ctx, team.Ref); err != nil || got.State != identity.IdentityRevoked || got.CreatedBy != adminA {
		t.Fatalf("get revoked: %+v %v", got, err)
	}
}

func TestService_TransitionTargetsMustBeVisibleToTheActor(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	svc := e.createService("req-2")
	_, err := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminB, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "team-b admin on team-a identity", err, identity.ErrForbidden)
	_, err = e.svc.Revoke(ctx, identity.TransitionRequest{Actor: adminA, Ref: svc.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "team admin on org service identity", err, identity.ErrForbidden)
	// Подделка владельца в ссылке не помогает: владелец выводится из ссылки и сверяется с хранимым.
	forged := team.Ref
	forged.TeamID = "team-b"
	_, err = e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminB, Ref: forged, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "forged team in ref", err, identity.ErrNotFound)
	// Несуществующая identity в области, где actor вправе, — ErrNotFound.
	ghost := identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "tid-ghost", OrgID: "org-a", TeamID: "team-a"}
	_, err = e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: ghost, ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "ghost", err, identity.ErrNotFound)
	if _, err := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u1"}, ExpectedRevision: 1, MutationID: e.mut()}); !errors.Is(err, identity.ErrInvalid) {
		t.Errorf("user principal is not a technical identity: %v", err)
	}
}

// ---------- binding ----------

func (e *env) assign(actor identity.ActorRef, p identity.PrincipalRef, cluster string) (identity.CredentialBinding, error) {
	return e.svc.AssignBinding(ctx, identity.AssignBindingRequest{Actor: actor, Principal: p, ClusterID: cluster, MutationID: e.mut()})
}

func TestService_Binding_ClusterOwnershipIsResolvedByTheServer(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	svc := e.createService("req-2")

	// Team principal — только кластер своей команды.
	_, err := e.assign(adminA, team.Ref, "cl-b")
	is(t, "team principal → other team's cluster", err, identity.ErrInvalid)
	// Кластер чужой организации неотличим от несуществующего.
	_, err = e.assign(adminA, team.Ref, "cl-other-org")
	is(t, "foreign org cluster", err, identity.ErrNotFound)
	_, err = e.assign(adminA, team.Ref, "cl-unknown")
	is(t, "unknown cluster", err, identity.ErrNotFound)
	_, err = e.assign(orgAdmin, svc.Ref, "cl-other-org")
	is(t, "service → foreign org cluster", err, identity.ErrNotFound)
	// Каталог недоступен или вернул непригодного владельца — отказ, а не допущение.
	e.clusters.err = errors.New("catalog down")
	_, err = e.assign(adminA, team.Ref, "cl-a")
	is(t, "catalog unavailable", err, identity.ErrUnavailable)
	e.clusters.err = nil
	e.clusters.owners["cl-weird"] = identity.OwnerScope{Kind: identity.OwnerOrganization, OrgID: "org-a"}
	_, err = e.assign(adminA, team.Ref, "cl-weird")
	is(t, "unusable owner from catalog", err, identity.ErrUnavailable)
	if n, _ := e.rec.count(); n != 2 {
		t.Fatalf("rejected assignments must not reach the audit journal or Backend, intents=%d", n)
	}
	if l, _ := e.file.ListBindings(ctx, team.Ref); len(l) != 0 {
		t.Fatal("rejected assignments stored bindings")
	}

	// Свой кластер — pending.
	b, err := e.assign(adminA, team.Ref, "cl-a")
	if err != nil || b.State != identity.BindingPending || b.Revision != 1 || b.Principal != team.Ref || b.CreatedBy != adminA {
		t.Fatalf("assign: %+v %v", b, err)
	}
	// Service principal — любой кластер своей организации (любой команды), и это не даёт прав само по себе.
	for _, c := range []string{"cl-a", "cl-b"} {
		if _, err := e.assign(orgAdmin, svc.Ref, c); err != nil {
			t.Fatalf("service → %s: %v", c, err)
		}
	}
	// Привязка service principal не затрагивает team principal.
	if l, _ := e.file.ListBindings(ctx, team.Ref); len(l) != 1 {
		t.Fatalf("bindings leaked across principals: %d", len(l))
	}
}

func TestService_Binding_Authorization(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	_, err := e.assign(adminB, team.Ref, "cl-a")
	is(t, "other team admin", err, identity.ErrForbidden)
	_, err = e.assign(orgAdmin, team.Ref, "cl-a")
	is(t, "org admin without team authority", err, identity.ErrForbidden)
	b, err := e.assign(adminA, team.Ref, "cl-a")
	if err != nil {
		t.Fatal(err)
	}
	if call := e.authz.calls[len(e.authz.calls)-1]; call.action != identity.AdminAssignCredential || call.owner != teamAScope {
		t.Fatalf("authz: %+v", call)
	}
	for name, f := range map[string]func() error{
		"activate by other team": func() error {
			_, err := e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminB, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: b.Revision, MutationID: e.mut()})
			return err
		},
		"revoke by other team": func() error {
			_, err := e.svc.RevokeBinding(ctx, identity.BindingTransitionRequest{Actor: adminB, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: b.Revision, MutationID: e.mut()})
			return err
		},
	} {
		is(t, name, f(), identity.ErrForbidden)
	}
	if got, _ := e.file.GetBinding(ctx, team.Ref, "cl-a"); got.State != identity.BindingPending || got.Revision != 1 {
		t.Fatalf("forbidden attempts changed the binding: %+v", got)
	}
}

func TestService_Binding_Lifecycle_PendingRevokedDeny_ConfirmedAuditRequired(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	b, err := e.assign(adminA, team.Ref, "cl-a")
	if err != nil {
		t.Fatal(err)
	}
	// Pending резервирует кортеж, но использовать его нельзя.
	_, err = e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	is(t, "pending binding", err, identity.ErrForbidden)
	_, err = e.assign(adminA, team.Ref, "cl-a")
	is(t, "second binding for the tuple", err, identity.ErrConflict)
	if o, _ := e.rec.GetOutcome(ctx, "org-a", e.rec.byAction("binding.create")[1].EventID); o.Result != audit.CriticalFailed || o.ReasonCode != "conflict" {
		t.Fatalf("rejected duplicate must be audited as failed: %+v", o)
	}

	act, err := e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 1, MutationID: "m-act"})
	if err != nil || act.State != identity.BindingActive || act.Revision != 2 || act.ID != b.ID {
		t.Fatalf("activate: %+v %v", act, err)
	}
	got, err := e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	if err != nil || got.ID != b.ID {
		t.Fatalf("active+confirmed binding must resolve: %+v %v", got, err)
	}
	// Идемпотентный повтор активации.
	if again, err := e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 1, MutationID: "m-act"}); err != nil || again.Revision != 2 {
		t.Fatalf("activate replay: %+v %v", again, err)
	}

	// Неподтверждённый аудит привязки блокирует использование.
	e.rec.mu.Lock()
	saved := e.rec.outcomes[act.LastAuditEventID]
	delete(e.rec.outcomes, act.LastAuditEventID)
	e.rec.mu.Unlock()
	_, err = e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	is(t, "binding with unconfirmed audit", err, identity.ErrForbidden)
	e.rec.mu.Lock()
	e.rec.outcomes[act.LastAuditEventID] = saved
	e.rec.mu.Unlock()

	// Приостановка/отзыв principal делают привязку непригодной, не трогая саму привязку.
	s, _ := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	_, err = e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	is(t, "suspended principal", err, identity.ErrForbidden)
	if _, err := e.svc.Resume(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: s.Revision, MutationID: e.mut()}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveBinding(ctx, team.Ref, "cl-a"); err != nil {
		t.Fatalf("after resume: %v", err)
	}

	rv, err := e.svc.RevokeBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 2, MutationID: "m-rev"})
	if err != nil || rv.State != identity.BindingRevoked || rv.Revision != 3 {
		t.Fatalf("revoke: %+v %v", rv, err)
	}
	_, err = e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	is(t, "revoked binding", err, identity.ErrRevoked)
	// Идемпотентный повтор отзыва узнаётся и после того, как живой привязки уже нет.
	if again, err := e.svc.RevokeBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 2, MutationID: "m-rev"}); err != nil || again.Revision != 3 {
		t.Fatalf("revoke replay: %+v %v", again, err)
	}
	// Отозванную привязку нельзя ни активировать, ни отозвать вновь.
	_, err = e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 3, MutationID: e.mut()})
	is(t, "activate revoked", err, identity.ErrRevoked)
	// Повторное назначение — новая привязка с новым ID; старая остаётся в истории.
	nb, err := e.assign(adminA, team.Ref, "cl-a")
	if err != nil || nb.ID == b.ID || nb.State != identity.BindingPending {
		t.Fatalf("re-assign: %+v %v", nb, err)
	}
	if l, _ := e.svc.ListBindings(ctx, team.Ref); len(l) != 2 {
		t.Fatalf("history: %d", len(l))
	}
	_, err = e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	is(t, "new pending binding", err, identity.ErrForbidden)
}

func TestService_Binding_AssignReplayAndStaleWriter(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	req := identity.AssignBindingRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", MutationID: "m-assign"}
	first, err := e.svc.AssignBinding(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	intents, _ := e.rec.count()
	again, err := e.svc.AssignBinding(ctx, req)
	if err != nil || again.ID != first.ID || again.Revision != 1 {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if n, _ := e.rec.count(); n != intents {
		t.Fatal("replay must not append intents")
	}
	// Потеря outcome → unknown → повтор завершает аудит без повторной мутации.
	e2 := newEnv(t)
	team2 := e2.createTeam("req-1")
	e2.rec.failOutcome = errors.New("journal write failed")
	r2 := identity.AssignBindingRequest{Actor: adminA, Principal: team2.Ref, ClusterID: "cl-a", MutationID: "m-assign"}
	_, err = e2.svc.AssignBinding(ctx, r2)
	is(t, "assign with lost outcome", err, audit.ErrOutcomeUnknown)
	e2.rec.failOutcome = nil
	m := e2.be.count()
	if got, err := e2.svc.AssignBinding(ctx, r2); err != nil || got.Revision != 1 || e2.be.count() != m {
		t.Fatalf("reconcile: %+v %v mutations %d→%d", got, err, m, e2.be.count())
	}
	// Устаревший писатель активации.
	_, err = e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 7, MutationID: e.mut()})
	is(t, "stale activate", err, identity.ErrConflict)
	_, err = e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-none", ExpectedRevision: 1, MutationID: e.mut()})
	is(t, "no binding", err, identity.ErrNotFound)
}

func TestService_Binding_RevokedOrSuspendedPrincipal(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	b, _ := e.assign(adminA, team.Ref, "cl-a")
	s, _ := e.svc.Suspend(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: 1, MutationID: e.mut()})
	// Активировать под приостановленной identity нельзя; назначать новые — тоже.
	_, err := e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: b.Revision, MutationID: e.mut()})
	is(t, "activate under suspended", err, identity.ErrInvalid)
	_, err = e.assign(adminA, team.Ref, "cl-b")
	is(t, "assign under suspended (other team cluster is rejected first)", err, identity.ErrInvalid)
	// Отозвать привязку администратор может всегда.
	if _, err := e.svc.RevokeBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: b.Revision, MutationID: e.mut()}); err != nil {
		t.Fatalf("revoke under suspended principal: %v", err)
	}
	_, err = e.svc.Revoke(ctx, identity.TransitionRequest{Actor: adminA, Ref: team.Ref, ExpectedRevision: s.Revision, MutationID: e.mut()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.assign(adminA, team.Ref, "cl-a")
	is(t, "assign under revoked", err, identity.ErrRevoked)
	_, err = e.svc.ResolveBinding(ctx, team.Ref, "cl-a")
	is(t, "resolve under revoked principal", err, identity.ErrRevoked)
}

// ---------- границы ответственности ----------

func TestService_IdentityAndAssignmentCreateNoGrantsAndStayInScope(t *testing.T) {
	e := newEnv(t)
	team := e.createTeam("req-1")
	svc := e.createService("req-2")
	b, _ := e.assign(adminA, team.Ref, "cl-a")
	e.svc.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: b.Revision, MutationID: e.mut()})
	e.assign(orgAdmin, svc.Ref, "cl-b")

	// Аудируются только действия identity и binding; никаких grants/delegation/credential/k8s.
	allowed := map[string]bool{"identity.create": true, "binding.create": true, "binding.activate": true}
	for _, id := range e.rec.order {
		in := e.rec.intents[id]
		if !allowed[in.Action] || (in.ResourceType != "identity" && in.ResourceType != "binding") {
			t.Errorf("unexpected audited action: %+v", in)
		}
	}
	for _, c := range e.authz.calls {
		if c.action != identity.AdminCreate && c.action != identity.AdminAssignCredential {
			t.Errorf("unexpected admin action %s", c.action)
		}
	}
	// Service identity не получает автоматически ничего у команд: список identity команды не изменился.
	if l, _ := e.svc.List(ctx, teamAScope); len(l) != 1 || l[0].Ref != team.Ref {
		t.Fatalf("team A identities: %+v", l)
	}
	if l, _ := e.svc.List(ctx, orgAScope); len(l) != 1 || l[0].Ref != svc.Ref {
		t.Fatalf("org identities: %+v", l)
	}
}

func TestService_NoSecretsInAuditOrErrors(t *testing.T) {
	e := newEnv(t)
	secretish := "s3cr3t-token-VALUE"
	// Секреты в display name — пользовательский текст: он не попадает в аудит и в тексты ошибок.
	got, err := e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: secretish, MutationID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	e.rec.mu.Lock()
	raw, _ := json.Marshal(struct {
		I map[string]audit.CriticalIntent
		O map[string]audit.CriticalOutcome
	}{e.rec.intents, e.rec.outcomes})
	e.rec.mu.Unlock()
	if strings.Contains(string(raw), secretish) {
		t.Fatalf("display name reached the audit journal: %s", raw)
	}
	_, err = e.svc.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: secretish + "x", MutationID: "req-1"})
	if err == nil || strings.Contains(err.Error(), secretish) {
		t.Fatalf("error text must not echo field values: %v", err)
	}
	_ = got
}

// ---------- реальный журнал + перезапуск ----------

func TestService_WithFileCriticalJournal_SurvivesRestart(t *testing.T) {
	dir, jdir := t.TempDir(), t.TempDir()
	open := func() (*identity.Service, *identity.FileBackend, *audit.FileCriticalRecorder) {
		fb, err := identity.OpenFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := audit.OpenCriticalFile(jdir)
		if err != nil {
			t.Fatal(err)
		}
		az := &fakeAuthz{grants: map[string][]identity.OwnerScope{"admin-a": {teamAScope}}}
		cl := &fakeClusters{owners: map[string]identity.OwnerScope{"cl-a": teamAScope}}
		s, err := identity.NewService(fb, rec, az, cl)
		if err != nil {
			t.Fatal(err)
		}
		return s, fb, rec
	}
	s, _, rec := open()
	team, err := s.Create(ctx, identity.CreateRequest{Actor: adminA, Kind: identity.PrincipalTeam, OrgID: "org-a", TeamID: "team-a", DisplayName: "bot", MutationID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignBinding(ctx, identity.AssignBindingRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", MutationID: "req-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateBinding(ctx, identity.BindingTransitionRequest{Actor: adminA, Principal: team.Ref, ClusterID: "cl-a", ExpectedRevision: 1, MutationID: "req-3"}); err != nil {
		t.Fatal(err)
	}
	rec.Close()

	s2, _, rec2 := open()
	defer rec2.Close()
	if p, _ := rec2.ListPending(ctx, "org-a", 10); len(p) != 0 {
		t.Fatalf("no pending intents expected after clean run: %d", len(p))
	}
	if _, err := s2.Resolve(ctx, team.Ref); err != nil {
		t.Fatalf("identity after restart: %v", err)
	}
	if b, err := s2.ResolveBinding(ctx, team.Ref, "cl-a"); err != nil || b.State != identity.BindingActive {
		t.Fatalf("binding after restart: %+v %v", b, err)
	}
	// Интегратор видит intent/outcome в организации-владельце и не видит в чужой.
	if o, err := rec2.GetOutcome(ctx, "org-a", team.LastAuditEventID); err != nil || o.Result != audit.CriticalSucceeded {
		t.Fatalf("outcome: %+v %v", o, err)
	}
	if _, err := rec2.GetOutcome(ctx, "org-b", team.LastAuditEventID); !errors.Is(err, audit.ErrCriticalNotFound) {
		t.Fatalf("foreign org: %v", err)
	}
}
