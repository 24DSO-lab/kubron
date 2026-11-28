// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/authorization"
	"github.com/24DSO-lab/kubron/internal/identity"
)

func createReq(w *world, mutationID string) authorization.CreateGrantRequest {
	return authorization.CreateGrantRequest{
		Actor: adminA, Principal: tpA, Actions: []authorization.OperationAction{restart}, Targets: org("c-a1"),
		ExpiresAt: w.future(), MutationID: mutationID,
	}
}

func TestService_CriticalAuditProtocol(t *testing.T) {
	w := newWorld(t)
	g, err := w.svc.CreateGrant(ctx, createReq(w, "mut-proto"))
	if err != nil {
		t.Fatal(err)
	}
	if got := w.rec.callLog(); len(got) != 2 || got[0] != "intent:grant.create" || got[1] != "outcome:succeeded" {
		t.Fatalf("protocol must be intent → outcome: %v", got)
	}
	in := w.rec.intents[g.LastAuditEventID]
	if in.MutationID != "mut-proto" || in.ResourceID != g.ID || in.ResourceType != "grant" || in.OrgID != "org-a" || in.TeamID != "team-a" ||
		in.PrincipalID != "tp-a" || in.ActorID != adminA.ID || in.ExpectedRevision != 0 || !strings.HasPrefix(in.RequestDigest, "sha256:") {
		t.Fatalf("intent fields: %+v", in)
	}
	out := w.rec.outcomes[g.LastAuditEventID]
	if out.Result != audit.CriticalSucceeded || out.ResourceRevision != g.Revision {
		t.Fatalf("outcome: %+v", out)
	}
	w.wantGranted(tpA, restart, "c-a1")

	// update/revoke: ожидаемая revision попадает в intent
	u, err := w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1,
		Actions: []authorization.OperationAction{scale}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: "mut-upd"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.rec.intents[u.LastAuditEventID]; got.Action != "grant.update" || got.ExpectedRevision != 1 {
		t.Fatalf("update intent: %+v", got)
	}
	r, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 2, MutationID: "mut-rev"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.rec.intents[r.LastAuditEventID]; got.Action != "grant.revoke" || got.ExpectedRevision != 2 {
		t.Fatalf("revoke intent: %+v", got)
	}
}

func TestService_IntentFailureBlocksMutation(t *testing.T) {
	w := newWorld(t)
	w.rec.failIntent = errors.New("audit store down")
	_, err := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	if !errors.Is(err, authorization.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if l, _ := w.backend.ListGrants(ctx, tpA); len(l) != 0 {
		t.Fatalf("backend must not be called without a durable intent: %+v", l)
	}
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantRequired)

	// то же для изменения существующего
	w.rec.failIntent = nil
	g, _ := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	w.rec.failIntent = errors.New("audit store down")
	if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1, MutationID: w.mut()}); !errors.Is(err, authorization.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if cur, _ := w.backend.GetGrant(ctx, g.ID); cur.State != authorization.GrantActive || cur.Revision != 1 {
		t.Fatalf("grant must not change: %+v", cur)
	}
}

// Потерянный outcome: успех не сообщается, запись непригодна; повтор доказательно завершает аудит без повторной мутации.
func TestService_LostOutcomeBlocksUseUntilReconciled(t *testing.T) {
	w := newWorld(t)
	req := createReq(w, "mut-lost")
	w.rec.failOutcome = errors.New("audit store down")
	_, err := w.svc.CreateGrant(ctx, req)
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("want ErrOutcomeUnknown, got %v", err)
	}
	l, _ := w.backend.ListGrants(ctx, tpA)
	if len(l) != 1 || l[0].Revision != 1 {
		t.Fatalf("mutation was applied exactly once: %+v", l)
	}
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantAuditUnconfirmed) // запись непригодна

	w.rec.failOutcome = nil
	intentsBefore := len(w.rec.intents)
	g, err := w.svc.CreateGrant(ctx, req) // тот же запрос
	if err != nil {
		t.Fatalf("replay must reconcile: %v", err)
	}
	if len(w.rec.intents) != intentsBefore || g.Revision != 1 {
		t.Fatalf("replay must neither append intents nor mutate: intents %d→%d rev=%d", intentsBefore, len(w.rec.intents), g.Revision)
	}
	w.wantGranted(tpA, restart, "c-a1")
}

func TestService_OutcomeUnavailableDuringReplayFailsClosed(t *testing.T) {
	w := newWorld(t)
	req := createReq(w, "mut-x")
	if _, err := w.svc.CreateGrant(ctx, req); err != nil {
		t.Fatal(err)
	}
	w.rec.getErr = errors.New("audit read down")
	if _, err := w.svc.CreateGrant(ctx, req); !errors.Is(err, authorization.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestService_RevocationTakesEffectEvenIfOutcomeUnknown(t *testing.T) {
	w := newWorld(t)
	g, _ := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	w.wantGranted(tpA, restart, "c-a1")
	w.rec.failOutcome = errors.New("audit store down")
	_, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1, MutationID: w.mut()})
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("want ErrOutcomeUnknown, got %v", err)
	}
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantRevoked) // отзыв не ждёт аудита
}

func TestService_UpdateWithLostOutcomeIsUnusable(t *testing.T) {
	w := newWorld(t)
	g, _ := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	w.rec.failOutcome = errors.New("audit store down")
	_, err := w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1,
		Actions: []authorization.OperationAction{restart, scale}, Targets: org("c-a1", "c-a2"), ExpiresAt: w.future(), MutationID: w.mut()})
	if !errors.Is(err, audit.ErrOutcomeUnknown) {
		t.Fatalf("want ErrOutcomeUnknown, got %v", err)
	}
	// расширенный grant нельзя использовать, пока его аудит не подтверждён (прежняя версия тоже заменена)
	w.wantDenied(tpA, scale, "c-a2", authorization.ReasonGrantAuditUnconfirmed)
}

func TestService_BackendRefusalIsAuditedAsFailed(t *testing.T) {
	w := newWorld(t)
	g, _ := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	_, err := w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 9,
		Actions: []authorization.OperationAction{scale}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: "mut-stale"})
	if !errors.Is(err, authorization.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	var failed *audit.CriticalOutcome
	for _, o := range w.rec.outcomes {
		if o.ReasonCode == "conflict" {
			o := o
			failed = &o
		}
	}
	if failed == nil || failed.Result != audit.CriticalFailed {
		t.Fatalf("a refused mutation must be audited as failed/conflict: %+v", w.rec.outcomes)
	}
	w.wantGranted(tpA, restart, "c-a1") // исходный grant не затронут
}

type flakyBackend struct {
	*authorization.FileBackend
	failUpdate atomic.Bool
}

func (f *flakyBackend) UpdateGrant(c context.Context, in authorization.Grant, rev uint64) (authorization.Grant, error) {
	if f.failUpdate.Load() {
		return authorization.Grant{}, errors.New("disk unplugged") // неясный исход
	}
	return f.FileBackend.UpdateGrant(c, in, rev)
}

func TestService_BackendUnavailableIsAuditedAsUnknown(t *testing.T) {
	w := newWorld(t)
	fb := &flakyBackend{FileBackend: w.backend}
	svc, err := authorization.NewService(fb, w.backend, w.rec, w.authz, w.cat, w.ids, authorization.WithClock(w.clk))
	if err != nil {
		t.Fatal(err)
	}
	g, err := svc.CreateGrant(ctx, createReq(w, w.mut()))
	if err != nil {
		t.Fatal(err)
	}
	fb.failUpdate.Store(true)
	_, err = svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1,
		Actions: []authorization.OperationAction{scale}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: w.mut()})
	if !errors.Is(err, authorization.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	unknown := 0
	for _, o := range w.rec.outcomes {
		if o.Result == audit.CriticalUnknown && o.ReasonCode == "backend_unavailable" {
			unknown++
		}
	}
	if unknown != 1 {
		t.Fatalf("unclear backend outcome must be audited as unknown: %+v", w.rec.outcomes)
	}
	if strings.Contains(err.Error(), "unplugged") {
		t.Fatalf("backend error text must not leak: %v", err)
	}
}

func TestService_IdempotencyAndConflicts(t *testing.T) {
	w := newWorld(t)
	req := createReq(w, "mut-1")
	g1, err := w.svc.CreateGrant(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	n := len(w.rec.callLog())
	g2, err := w.svc.CreateGrant(ctx, req) // повтор
	if err != nil || g2.ID != g1.ID || g2.Revision != 1 || len(w.rec.callLog()) != n {
		t.Fatalf("exact replay must be a no-op: %+v err=%v calls=%d→%d", g2, err, n, len(w.rec.callLog()))
	}
	// порядок и дубликаты в списках не меняют содержимое запроса
	same := req
	same.Targets = authorization.TargetScope{OrgID: "org-a", ClusterIDs: []string{"c-a1", "c-a1"}}
	if g3, err := w.svc.CreateGrant(ctx, same); err != nil || g3.ID != g1.ID {
		t.Fatalf("canonical-equal request must replay: %+v err=%v", g3, err)
	}
	diff := req
	diff.Actions = []authorization.OperationAction{scale}
	if _, err := w.svc.CreateGrant(ctx, diff); !errors.Is(err, authorization.ErrConflict) {
		t.Fatalf("same mutation id with different content must conflict: %v", err)
	}
	other := req
	other.Actor = adminB // чужой Team Admin не может присвоить чужую мутацию
	if _, err := w.svc.CreateGrant(ctx, other); !errors.Is(err, authorization.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	// update-replay и reuse
	u := authorization.UpdateGrantRequest{Actor: adminA, ID: g1.ID, ExpectedRevision: 1, Actions: []authorization.OperationAction{scale},
		Targets: org("c-a1"), ExpiresAt: g1.ExpiresAt, MutationID: "mut-u"}
	first, err := w.svc.UpdateGrant(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	again, err := w.svc.UpdateGrant(ctx, u)
	if err != nil || again.Revision != first.Revision {
		t.Fatalf("update replay: %+v err=%v", again, err)
	}
	u.Actions = []authorization.OperationAction{del}
	if _, err := w.svc.UpdateGrant(ctx, u); !errors.Is(err, authorization.ErrConflict) {
		t.Fatalf("update mutation id reuse: %v", err)
	}
}

func TestService_RevokedIsTerminal(t *testing.T) {
	w := newWorld(t)
	g, _ := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	r, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1, MutationID: w.mut()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: r.Revision,
		Actions: []authorization.OperationAction{restart}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: w.mut()})
	if !errors.Is(err, authorization.ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: r.Revision, MutationID: w.mut()}); !errors.Is(err, authorization.ErrRevoked) {
		t.Fatalf("revoke revoked: %v", err)
	}
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantRevoked)
}

func TestService_PreconditionsAndAuthorizerFailures(t *testing.T) {
	w := newWorld(t)
	// срок обязан быть в будущем
	req := createReq(w, w.mut())
	req.ExpiresAt = w.clk.Now()
	if _, err := w.svc.CreateGrant(ctx, req); !errors.Is(err, authorization.ErrInvalid) {
		t.Errorf("past expiry: %v", err)
	}
	// неизвестный и отозванный principal
	req = createReq(w, w.mut())
	req.Principal = identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ghost", OrgID: "org-a", TeamID: "team-a"}
	if _, err := w.svc.CreateGrant(ctx, req); !errors.Is(err, authorization.ErrNotFound) {
		t.Errorf("unknown principal: %v", err)
	}
	w.ids.set(tpA, identity.IdentityRevoked)
	if _, err := w.svc.CreateGrant(ctx, createReq(w, w.mut())); !errors.Is(err, authorization.ErrRevoked) {
		t.Errorf("revoked principal: %v", err)
	}
	w.ids.set(tpA, identity.IdentityActive)
	// сбой авторизатора — недоступность, а не «разрешено»
	w.authz.err = errors.New("rbac down")
	if _, err := w.svc.CreateGrant(ctx, createReq(w, w.mut())); !errors.Is(err, authorization.ErrUnavailable) {
		t.Errorf("authorizer failure: %v", err)
	}
	w.authz.err = nil
	// некорректные идентификаторы
	for name, r := range map[string]authorization.CreateGrantRequest{
		"empty mutation id": func() authorization.CreateGrantRequest { r := createReq(w, ""); return r }(),
		"bad mutation id":   func() authorization.CreateGrantRequest { r := createReq(w, "a b"); return r }(),
	} {
		if _, err := w.svc.CreateGrant(ctx, r); !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: "nope", ExpectedRevision: 1, MutationID: w.mut()}); !errors.Is(err, authorization.ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: "nope", ExpectedRevision: 0, MutationID: w.mut()}); !errors.Is(err, authorization.ErrInvalid) {
		t.Errorf("zero expected revision: %v", err)
	}
	// чужой Team Admin не может отозвать чужой grant
	g, _ := w.svc.CreateGrant(ctx, createReq(w, w.mut()))
	if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminB, ID: g.ID, ExpectedRevision: 1, MutationID: w.mut()}); !errors.Is(err, authorization.ErrForbidden) {
		t.Errorf("foreign admin revoke: %v", err)
	}
}

func TestService_EventIDFailureBlocksMutation(t *testing.T) {
	w := newWorld(t)
	svc, err := authorization.NewService(w.backend, w.backend, w.rec, w.authz, w.cat, w.ids, authorization.WithClock(w.clk),
		authorization.WithEventIDs(func() (string, error) { return "", errors.New("entropy exhausted") }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateGrant(ctx, createReq(w, w.mut())); !errors.Is(err, authorization.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if l, _ := w.backend.ListGrants(ctx, tpA); len(l) != 0 {
		t.Fatalf("no mutation without an event id")
	}
}

func TestService_DelegationAuditAndTransitions(t *testing.T) {
	w := newWorld(t)
	d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}})
	in := w.rec.intents[d.LastAuditEventID]
	if in.Action != "delegation.create" || in.ResourceType != "delegation" || in.TeamID != "team-a" || in.PrincipalID != "sp-1" || in.PrincipalKind != "service" {
		t.Fatalf("delegation intent: %+v", in)
	}
	// resume без suspend — недопустимый переход; suspend дважды — тоже
	if _, err := w.svc.ResumeDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: 1, MutationID: w.mut()}); !errors.Is(err, authorization.ErrInvalid) {
		t.Errorf("resume active: %v", err)
	}
	s, err := w.svc.SuspendDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: 1, MutationID: "mut-s"})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := w.svc.SuspendDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: 1, MutationID: "mut-s"}); err != nil || again.Revision != s.Revision {
		t.Errorf("suspend replay: %+v err=%v", again, err)
	}
	if _, err := w.svc.SuspendDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: s.Revision, MutationID: w.mut()}); !errors.Is(err, authorization.ErrInvalid) {
		t.Errorf("suspend suspended: %v", err)
	}
	// сервис отозван → delegation ему не выдаётся
	w.ids.set(svc2, identity.IdentityRevoked)
	_, err = w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{Actor: adminA, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: svc2,
		Actions: []authorization.OperationAction{restart}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: w.mut()})
	if !errors.Is(err, authorization.ErrRevoked) {
		t.Errorf("revoked service: %v", err)
	}
	// delegation в приостановленном состоянии можно изменить, оно сохраняется
	u, err := w.svc.UpdateDelegation(ctx, authorization.UpdateDelegationRequest{Actor: adminA, ID: d.ID, ExpectedRevision: s.Revision,
		Actions: []authorization.OperationAction{scale}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: w.mut()})
	if err != nil || u.State != authorization.DelegationSuspended {
		t.Fatalf("update keeps state: %+v err=%v", u, err)
	}
}

func TestNewConstructorsRejectNilDependencies(t *testing.T) {
	w := newWorld(t)
	if _, err := authorization.NewService(nil, w.backend, w.rec, w.authz, w.cat, w.ids); err == nil {
		t.Error("nil grants")
	}
	if _, err := authorization.NewService(w.backend, w.backend, nil, w.authz, w.cat, w.ids); err == nil {
		t.Error("nil recorder: audit must not be optional")
	}
	if _, err := authorization.NewService(w.backend, w.backend, w.rec, nil, w.cat, w.ids); err == nil {
		t.Error("nil authorizer")
	}
	full := authorization.Deps{Grants: w.backend, Delegations: w.backend, Owners: w.cat, Principals: w.ids, Actions: w.cat, Outcomes: w.rec, Clock: w.clk}
	for name, mut := range map[string]func(*authorization.Deps){
		"grants": func(d *authorization.Deps) { d.Grants = nil }, "delegations": func(d *authorization.Deps) { d.Delegations = nil },
		"owners": func(d *authorization.Deps) { d.Owners = nil }, "principals": func(d *authorization.Deps) { d.Principals = nil },
		"actions": func(d *authorization.Deps) { d.Actions = nil }, "outcomes": func(d *authorization.Deps) { d.Outcomes = nil },
		"clock": func(d *authorization.Deps) { d.Clock = nil },
	} {
		d := full
		mut(&d)
		if _, err := authorization.NewEvaluator(d); err == nil {
			t.Errorf("nil %s must be rejected", name)
		}
	}
}

type stubResolver struct{ err error }

func (s stubResolver) Resolve(context.Context, identity.PrincipalRef) (identity.Identity, error) {
	return identity.Identity{}, s.err
}

func TestIdentityStateReaderAdapter(t *testing.T) {
	cases := []struct {
		err   error
		state identity.IdentityState
		want  error
	}{
		{nil, identity.IdentityActive, nil},
		{identity.ErrRevoked, identity.IdentityRevoked, nil},
		{identity.ErrForbidden, identity.IdentitySuspended, nil}, // приостановлена либо аудит не подтверждён
		{identity.ErrNotFound, "", authorization.ErrNotFound},
		{errors.New("db down"), "", authorization.ErrUnavailable},
	}
	for _, c := range cases {
		st, err := authorization.NewIdentityStateReader(stubResolver{c.err}).GetPrincipalState(ctx, tpA)
		if st != c.state || !errors.Is(err, c.want) && (c.want != nil || err != nil) {
			t.Errorf("%v → (%q, %v), want (%q, %v)", c.err, st, err, c.state, c.want)
		}
	}
}
