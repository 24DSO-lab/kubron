// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/authorization"
	"github.com/24DSO-lab/kubron/internal/identity"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	tpA  = identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "tp-a", OrgID: "org-a", TeamID: "team-a"}
	tpB  = identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "tp-b", OrgID: "org-a", TeamID: "team-b"}
	svc1 = identity.PrincipalRef{Kind: identity.PrincipalService, ID: "sp-1", OrgID: "org-a"}
	svc2 = identity.PrincipalRef{Kind: identity.PrincipalService, ID: "sp-2", OrgID: "org-a"}

	restart = authorization.OperationAction{OperationID: "workload", ActionID: "restart"}
	scale   = authorization.OperationAction{OperationID: "workload", ActionID: "scale"}
	del     = authorization.OperationAction{OperationID: "workload", ActionID: "delete"}

	adminA   = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-a"}   // Team Admin team-a
	adminB   = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-b"}   // Team Admin team-b
	orgAdmin = identity.ActorRef{Kind: identity.ActorUser, ID: "org-admin"} // Organization Admin org-a
)

// ---------- часы ----------

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// ---------- каталог (ownership + action facts) ----------

type catalog struct {
	mu       sync.Mutex
	clusters map[string]authorization.TargetFacts
	actions  map[authorization.OperationAction]authorization.ActionFacts
	ownErr   error
	actErr   error
	// shared имитирует «Target Group, расшаренную команде»: это знание каталога, которое evaluator
	// использовать не вправе (ADR-0037 п. 9).
	shared map[string][]string
}

func (c *catalog) ResolveTargetFacts(_ context.Context, t authorization.TargetRef) (authorization.TargetFacts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ownErr != nil {
		return authorization.TargetFacts{}, c.ownErr
	}
	f, ok := c.clusters[t.ClusterID]
	if !ok {
		return authorization.TargetFacts{}, authorization.ErrNotFound
	}
	return f, nil
}

func (c *catalog) ResolveActionFacts(_ context.Context, op, act string) (authorization.ActionFacts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.actErr != nil {
		return authorization.ActionFacts{}, c.actErr
	}
	f, ok := c.actions[authorization.OperationAction{OperationID: op, ActionID: act}]
	if !ok {
		return authorization.ActionFacts{}, authorization.ErrNotFound
	}
	return f, nil
}

// ---------- identity ----------

type principals struct {
	mu     sync.Mutex
	states map[identity.PrincipalRef]identity.IdentityState
	err    error
	hook   func()
}

func (p *principals) GetPrincipalState(_ context.Context, r identity.PrincipalRef) (identity.IdentityState, error) {
	p.mu.Lock()
	hook, err := p.hook, p.err
	st, ok := p.states[r]
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return "", err
	}
	if !ok {
		return "", identity.ErrNotFound
	}
	return st, nil
}

func (p *principals) set(r identity.PrincipalRef, s identity.IdentityState) {
	p.mu.Lock()
	p.states[r] = s
	p.mu.Unlock()
}

// ---------- критичный аудит ----------

type recorder struct {
	mu          sync.Mutex
	intents     map[string]audit.CriticalIntent
	outcomes    map[string]audit.CriticalOutcome
	failIntent  error
	failOutcome error
	getErr      error
	calls       []string
}

func newRecorder() *recorder {
	return &recorder{intents: map[string]audit.CriticalIntent{}, outcomes: map[string]audit.CriticalOutcome{}}
}

func (r *recorder) AppendIntent(_ context.Context, in audit.CriticalIntent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failIntent != nil {
		return r.failIntent
	}
	if err := in.Validate(); err != nil { // реальная валидация audit: наши intent'ы обязаны быть аудит-безопасны
		return err
	}
	if old, ok := r.intents[in.EventID]; ok {
		if old != in {
			return audit.ErrCriticalConflict
		}
		return nil
	}
	r.intents[in.EventID] = in
	r.calls = append(r.calls, "intent:"+in.Action)
	return nil
}

func (r *recorder) AppendOutcome(c context.Context, out audit.CriticalOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := c.Err(); err != nil {
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
	r.calls = append(r.calls, "outcome:"+string(out.Result))
	return nil
}

func (r *recorder) GetOutcome(_ context.Context, orgID, eventID string) (audit.CriticalOutcome, error) {
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

func (r *recorder) ListPending(context.Context, string, int) ([]audit.CriticalIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []audit.CriticalIntent
	for id, in := range r.intents {
		if _, done := r.outcomes[id]; !done {
			out = append(out, in)
		}
	}
	return out, nil
}

func (r *recorder) callLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// ---------- AdminAuthorizer ----------

// authz имитирует app-RBAC: Team Admin управляет grants Team-принципалов и delegation своей Team;
// Organization Admin — grants Service-принципалов своей Organization, но НЕ delegation за Team.
type authz struct {
	mu         sync.Mutex
	teamAdmins map[string]string
	orgAdmins  map[string]string
	err        error
	calls      []string
}

func (a *authz) AuthorizeGrantAdmin(_ context.Context, actor identity.ActorRef, p identity.PrincipalRef, action authorization.AdminAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "grant:"+string(action))
	if a.err != nil {
		return a.err
	}
	switch p.Kind {
	case identity.PrincipalTeam:
		if a.teamAdmins[actor.ID] == p.TeamID {
			return nil
		}
	case identity.PrincipalService:
		if a.orgAdmins[actor.ID] == p.OrgID {
			return nil
		}
	}
	return authorization.ErrForbidden
}

func (a *authz) AuthorizeDelegationAdmin(_ context.Context, actor identity.ActorRef, ownerTeamID string, action authorization.AdminAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "delegation:"+ownerTeamID+":"+string(action))
	if a.err != nil {
		return a.err
	}
	if a.teamAdmins[actor.ID] == ownerTeamID {
		return nil
	}
	return authorization.ErrForbidden
}

func (a *authz) callLog() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

// ---------- «мир» ----------

type world struct {
	t       *testing.T
	backend *authorization.FileBackend
	rec     *recorder
	cat     *catalog
	ids     *principals
	authz   *authz
	clk     *clock
	svc     *authorization.Service
	ev      *authorization.GrantEvaluator
	seq     atomic.Uint64
}

func newWorld(t *testing.T) *world {
	t.Helper()
	b, err := authorization.OpenFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &world{
		t: t, backend: b, rec: newRecorder(), clk: &clock{t: t0},
		cat: &catalog{
			clusters: map[string]authorization.TargetFacts{
				"c-a1": {OrgID: "org-a", TeamID: "team-a", Environment: "prod"},
				"c-a2": {OrgID: "org-a", TeamID: "team-a", Environment: "staging"},
				"c-b1": {OrgID: "org-a", TeamID: "team-b", Environment: "prod"},
				"c-x1": {OrgID: "org-b", TeamID: "team-x", Environment: "prod"},
			},
			actions: map[authorization.OperationAction]authorization.ActionFacts{
				restart: {}, scale: {}, del: {Destructive: true},
			},
			shared: map[string][]string{},
		},
		ids: &principals{states: map[identity.PrincipalRef]identity.IdentityState{
			tpA: identity.IdentityActive, tpB: identity.IdentityActive,
			svc1: identity.IdentityActive, svc2: identity.IdentityActive,
		}},
		authz: &authz{
			teamAdmins: map[string]string{adminA.ID: "team-a", adminB.ID: "team-b"},
			orgAdmins:  map[string]string{orgAdmin.ID: "org-a"},
		},
	}
	var evt atomic.Uint64
	w.svc, err = authorization.NewService(b, b, w.rec, w.authz, w.cat, w.ids, authorization.WithClock(w.clk),
		authorization.WithEventIDs(func() (string, error) { return fmt.Sprintf("evt-%d", evt.Add(1)), nil }))
	if err != nil {
		t.Fatal(err)
	}
	w.ev = w.evaluator(b, b)
	return w
}

func (w *world) evaluator(g authorization.GrantReader, d authorization.DelegationReader) *authorization.GrantEvaluator {
	w.t.Helper()
	ev, err := authorization.NewEvaluator(authorization.Deps{
		Grants: g, Delegations: d, Owners: w.cat, Principals: w.ids, Actions: w.cat, Outcomes: w.rec, Clock: w.clk,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return ev
}

func (w *world) mut() string { return fmt.Sprintf("mut-%d", w.seq.Add(1)) }

func (w *world) future() time.Time { return w.clk.Now().Add(24 * time.Hour) }

// adminFor возвращает человека, который по app-RBAC вправе выдавать grants principal'у.
func adminFor(p identity.PrincipalRef) identity.ActorRef {
	if p.Kind == identity.PrincipalTeam {
		if p.TeamID == "team-a" {
			return adminA
		}
		return adminB
	}
	return orgAdmin
}

func org(clusters ...string) authorization.TargetScope {
	return authorization.TargetScope{OrgID: "org-a", ClusterIDs: clusters}
}

// grant создаёт grant через Service (с полным критичным аудитом).
func (w *world) grant(p identity.PrincipalRef, actions []authorization.OperationAction, scope authorization.TargetScope,
	c authorization.ConstraintSet) authorization.Grant {
	w.t.Helper()
	g, err := w.svc.CreateGrant(ctx, authorization.CreateGrantRequest{
		Actor: adminFor(p), Principal: p, Actions: actions, Targets: scope, Constraints: c,
		ExpiresAt: w.future(), MutationID: w.mut(),
	})
	if err != nil {
		w.t.Fatalf("CreateGrant: %v", err)
	}
	return g
}

// delegate создаёт delegation от Team-администратора владельца.
func (w *world) delegate(team string, service identity.PrincipalRef, actions []authorization.OperationAction,
	scope authorization.TargetScope) authorization.Delegation {
	w.t.Helper()
	actor := adminA
	if team == "team-b" {
		actor = adminB
	}
	d, err := w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{
		Actor: actor, OrgID: "org-a", OwnerTeamID: team, ServicePrincipal: service, Actions: actions, Targets: scope,
		ExpiresAt: w.future(), MutationID: w.mut(),
	})
	if err != nil {
		w.t.Fatalf("CreateDelegation: %v", err)
	}
	return d
}

func (w *world) eval(p identity.PrincipalRef, a authorization.OperationAction, cluster string) (authorization.GrantDecision, error) {
	return w.ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{
		Principal: p, OperationID: a.OperationID, ActionID: a.ActionID, Target: authorization.TargetRef{ClusterID: cluster},
	})
}

// wantGranted/wantDenied проверяют решение; отказ обязан быть и с Granted=false, и с ожидаемым кодом.
func (w *world) wantGranted(p identity.PrincipalRef, a authorization.OperationAction, cluster string) authorization.GrantDecision {
	w.t.Helper()
	d, err := w.eval(p, a, cluster)
	if err != nil || !d.Granted || d.ReasonCode != authorization.ReasonGranted {
		w.t.Fatalf("want granted, got %+v err=%v", d, err)
	}
	return d
}

func (w *world) wantDenied(p identity.PrincipalRef, a authorization.OperationAction, cluster, reason string) authorization.GrantDecision {
	w.t.Helper()
	d, err := w.eval(p, a, cluster)
	if err != nil {
		w.t.Fatalf("deny must not be an error: %v", err)
	}
	if d.Granted || d.ReasonCode != reason || len(d.GrantIDs) != 0 || len(d.GrantRevisions) != 0 ||
		d.DelegationID != "" || d.DelegationRevision != 0 {
		w.t.Fatalf("want denied with %q, got %+v", reason, d)
	}
	return d
}
