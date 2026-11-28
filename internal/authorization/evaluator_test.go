// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/authorization"
	"github.com/24DSO-lab/kubron/internal/identity"
)

func TestEvaluator_NoGrantDenies(t *testing.T) {
	w := newWorld(t)
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantRequired)
	w.wantDenied(tpB, restart, "c-b1", authorization.ReasonGrantRequired)
}

func TestEvaluator_GrantAllowsExactTuple(t *testing.T) {
	w := newWorld(t)
	g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	d := w.wantGranted(tpA, restart, "c-a1")
	if len(d.GrantIDs) != 1 || d.GrantIDs[0] != g.ID || d.GrantRevisions[0] != g.Revision ||
		d.DelegationID != "" || d.DelegationRevision != 0 {
		t.Fatalf("decision must carry grant ID/revision: %+v", d)
	}
	w.wantDenied(tpA, scale, "c-a1", authorization.ReasonGrantRequired)   // другое действие
	w.wantDenied(tpA, restart, "c-a2", authorization.ReasonGrantRequired) // другой кластер той же Team
}

// Пары OperationID+ActionID точные: отдельные списки не дают декартова произведения.
func TestEvaluator_ExactPairsNotCartesian(t *testing.T) {
	w := newWorld(t)
	other := authorization.OperationAction{OperationID: "node", ActionID: "cordon"}
	crossA := authorization.OperationAction{OperationID: "workload", ActionID: "cordon"}
	crossB := authorization.OperationAction{OperationID: "node", ActionID: "restart"}
	w.cat.actions[other], w.cat.actions[crossA], w.cat.actions[crossB] = authorization.ActionFacts{}, authorization.ActionFacts{}, authorization.ActionFacts{}

	w.grant(tpA, []authorization.OperationAction{restart, other}, org("c-a1"), authorization.ConstraintSet{})
	w.wantGranted(tpA, restart, "c-a1")
	w.wantGranted(tpA, other, "c-a1")
	w.wantDenied(tpA, crossA, "c-a1", authorization.ReasonGrantRequired) // workload × cordon
	w.wantDenied(tpA, crossB, "c-a1", authorization.ReasonGrantRequired) // node × restart
}

func TestEvaluator_GrantUnionOrderIndependent(t *testing.T) {
	w := newWorld(t)
	g1 := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	g2 := w.grant(tpA, []authorization.OperationAction{restart, scale}, org("c-a1", "c-a2"), authorization.ConstraintSet{})
	g3 := w.grant(tpA, []authorization.OperationAction{scale}, org("c-a2"), authorization.ConstraintSet{})

	want := w.wantGranted(tpA, restart, "c-a1")
	if len(want.GrantIDs) != 2 {
		t.Fatalf("both applicable grants must qualify: %+v", want)
	}
	all, _ := w.backend.ListGrants(ctx, tpA)
	for i := 0; i < 24; i++ {
		perm := permute(all, i)
		ev := w.evaluator(fixedGrants{perm, w.backend}, w.backend)
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{
			Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d changed the decision:\n got %+v\nwant %+v (err=%v)", i, got, want, err)
		}
	}
	// union: g3 один даёт scale на c-a2, но не restart
	w.wantGranted(tpA, scale, "c-a2")
	w.wantGranted(tpA, restart, "c-a2") // restart на c-a2 даёт g2 (union), g3 тут не нужен
	// g3 в одиночку не даёт restart: убираем g2 — остаётся только scale на c-a2
	if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g2.ID, ExpectedRevision: g2.Revision, MutationID: w.mut()}); err != nil {
		t.Fatal(err)
	}
	w.wantDenied(tpA, restart, "c-a2", authorization.ReasonGrantRevoked) // применимый, но отозванный
	w.wantGranted(tpA, scale, "c-a2")
	w.wantGranted(tpA, restart, "c-a1") // g1 остаётся
	_ = g1
	_ = g3
}

// fixedGrants возвращает фиксированный порядок записей (backend остаётся источником для GetGrant).
type fixedGrants struct {
	list []authorization.Grant
	authorization.GrantReader
}

func (f fixedGrants) ListGrants(context.Context, identity.PrincipalRef) ([]authorization.Grant, error) {
	return append([]authorization.Grant(nil), f.list...), nil
}

func permute[T any](in []T, seed int) []T {
	out := append([]T(nil), in...)
	n := len(out)
	for i := n - 1; i > 0; i-- {
		j := (seed*7 + i*13 + 3) % (i + 1)
		out[i], out[j] = out[j], out[i]
		seed = seed/2 + 1
	}
	return out
}

// Constraints внутри одного grant объединяются по AND и не смешиваются между grants.
func TestEvaluator_ConstraintsAndedWithinGrant(t *testing.T) {
	t.Run("each constraint must hold", func(t *testing.T) {
		w := newWorld(t)
		nb := t0.Add(time.Hour)
		w.grant(tpA, []authorization.OperationAction{restart, del}, org("c-a1", "c-a2"), authorization.ConstraintSet{
			AllowedEnvironments: []string{"prod"}, DenyDestructive: true, NotBefore: &nb,
		})
		w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantConstraintNotMet) // NotBefore ещё не наступил
		w.clk.Set(nb)                                                                 // NotBefore включителен
		w.wantGranted(tpA, restart, "c-a1")
		w.wantDenied(tpA, restart, "c-a2", authorization.ReasonGrantConstraintNotMet) // staging вне AllowedEnvironments
		w.wantDenied(tpA, del, "c-a1", authorization.ReasonGrantConstraintNotMet)     // destructive запрещён
	})
	t.Run("not before one nanosecond early", func(t *testing.T) {
		w := newWorld(t)
		nb := t0.Add(time.Hour)
		w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{NotBefore: &nb})
		w.clk.Set(nb.Add(-time.Nanosecond))
		w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantConstraintNotMet)
		w.clk.Set(nb)
		w.wantGranted(tpA, restart, "c-a1")
	})
	t.Run("constraints do not mix across grants", func(t *testing.T) {
		w := newWorld(t)
		// g1: только prod, g2: только destructive-safe. Деструктивное действие на staging не проходит ни одним:
		// «prod из g1» и «DenyDestructive из g2» не складываются в один grant.
		w.grant(tpA, []authorization.OperationAction{del}, org("c-a1", "c-a2"), authorization.ConstraintSet{AllowedEnvironments: []string{"prod"}})
		w.grant(tpA, []authorization.OperationAction{del}, org("c-a1", "c-a2"), authorization.ConstraintSet{DenyDestructive: true})
		w.wantGranted(tpA, del, "c-a1") // g1: prod — разрешает (union)
		w.wantDenied(tpA, del, "c-a2", authorization.ReasonGrantConstraintNotMet)
	})
	t.Run("environment unknown to catalog fails closed", func(t *testing.T) {
		w := newWorld(t)
		w.cat.clusters["c-a1"] = authorization.TargetFacts{OrgID: "org-a", TeamID: "team-a"} // окружение не задано
		w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{AllowedEnvironments: []string{"prod"}})
		w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantConstraintNotMet)
	})
}

func TestEvaluator_ExpiryIsExclusive(t *testing.T) {
	w := newWorld(t)
	g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w.clk.Set(g.ExpiresAt.Add(-time.Nanosecond))
	w.wantGranted(tpA, restart, "c-a1")
	w.clk.Set(g.ExpiresAt)
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantExpired)
	w.clk.Set(g.ExpiresAt.Add(time.Hour))
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantExpired)
}

// Revoke/Update видны на следующей оценке: положительного кеша нет.
func TestEvaluator_NoStalePositiveCache(t *testing.T) {
	w := newWorld(t)
	g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w.wantGranted(tpA, restart, "c-a1")
	w.wantGranted(tpA, restart, "c-a1")

	upd, err := w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{
		Actor: adminA, ID: g.ID, ExpectedRevision: 1, Actions: []authorization.OperationAction{scale}, Targets: org("c-a1"),
		ExpiresAt: w.future(), MutationID: w.mut(),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantRequired) // изменение действует сразу
	d := w.wantGranted(tpA, scale, "c-a1")
	if d.GrantRevisions[0] != upd.Revision {
		t.Fatalf("decision must carry the current revision: %+v", d)
	}
	if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: upd.Revision, MutationID: w.mut()}); err != nil {
		t.Fatal(err)
	}
	w.wantDenied(tpA, scale, "c-a1", authorization.ReasonGrantRevoked)

	// принципал, ставший suspended/revoked, теряет всё немедленно
	w2 := newWorld(t)
	w2.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w2.wantGranted(tpA, restart, "c-a1")
	w2.ids.set(tpA, identity.IdentitySuspended)
	w2.wantDenied(tpA, restart, "c-a1", authorization.ReasonPrincipalNotActive)
	w2.ids.set(tpA, identity.IdentityRevoked)
	w2.wantDenied(tpA, restart, "c-a1", authorization.ReasonPrincipalNotActive)
}

// ---------- границы: TeamPrincipal ----------

func TestEvaluator_TeamPrincipalStrictlyOwnTeam(t *testing.T) {
	w := newWorld(t)
	w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w.wantGranted(tpA, restart, "c-a1")
	// чужой Team-кластер недостижим, даже если бы grant на него существовал (хранилище не даст его создать)
	w.wantDenied(tpA, restart, "c-b1", authorization.ReasonTargetNotAccessible)
	w.wantDenied(tpA, restart, "c-x1", authorization.ReasonTargetNotAccessible) // чужая Organization

	// Service-grant на чужую Team не помогает TeamPrincipal: delegation не расширяет TeamPrincipal
	w.grant(tpB, []authorization.OperationAction{restart}, org("c-b1"), authorization.ConstraintSet{})
	w.wantDenied(tpA, restart, "c-b1", authorization.ReasonTargetNotAccessible)
	w.wantGranted(tpB, restart, "c-b1")
}

func TestService_CannotGrantTeamPrincipalOtherTeam(t *testing.T) {
	w := newWorld(t)
	for name, scope := range map[string]authorization.TargetScope{
		"other team id":      {OrgID: "org-a", TeamIDs: []string{"team-b"}},
		"own and other team": {OrgID: "org-a", TeamIDs: []string{"team-a", "team-b"}},
		"other team cluster": org("c-a1", "c-b1"),
		"foreign org":        org("c-x1"),
		"unknown cluster":    org("c-nope"),
	} {
		_, err := w.svc.CreateGrant(ctx, authorization.CreateGrantRequest{
			Actor: adminA, Principal: tpA, Actions: []authorization.OperationAction{restart}, Targets: scope,
			ExpiresAt: w.future(), MutationID: w.mut(),
		})
		if !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if l, _ := w.backend.ListGrants(ctx, tpA); len(l) != 0 {
		t.Fatalf("rejected grants must not be stored")
	}
}

// Даже повреждённые/подменённые данные хранилища не расширяют TeamPrincipal: evaluator сам отвергает чужую Team.
func TestEvaluator_RejectsMalformedStoredGrants(t *testing.T) {
	base := func() authorization.Grant {
		return authorization.Grant{
			ID: "grt-1", Principal: tpA, Actions: []authorization.OperationAction{restart}, State: authorization.GrantActive,
			Targets: authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}}, Revision: 1,
			ExpiresAt: t0.Add(time.Hour), CreatedBy: adminA, LastMutationID: "mut-1", LastAuditEventID: "evt-1",
		}
	}
	cases := map[string]func(*authorization.Grant){
		"own plus foreign team": func(g *authorization.Grant) { g.Targets.TeamIDs = []string{"team-a", "team-b"} },
		"scope org differs":     func(g *authorization.Grant) { g.Targets.OrgID = "org-b" },
		"unbounded empty org":   func(g *authorization.Grant) { g.Targets.OrgID = "" },
		"no selectors":          func(g *authorization.Grant) { g.Targets = authorization.TargetScope{OrgID: "org-a"} },
		"object without uid": func(g *authorization.Grant) {
			g.Targets = authorization.TargetScope{OrgID: "org-a", Objects: []authorization.ObjectRef{{ClusterID: "c-a1"}}}
		},
		"unknown state":      func(g *authorization.Grant) { g.State = "weird" },
		"zero revision":      func(g *authorization.Grant) { g.Revision = 0 },
		"empty actions pair": func(g *authorization.Grant) { g.Actions = []authorization.OperationAction{{OperationID: "workload"}} },
		"no expiry":          func(g *authorization.Grant) { g.ExpiresAt = time.Time{} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			g := base()
			mut(&g)
			// подтверждённый аудит, чтобы отказ был именно из-за формы записи
			w.rec.intents["evt-1"] = audit.CriticalIntent{EventID: "evt-1", OrgID: "org-a"}
			w.rec.outcomes["evt-1"] = audit.CriticalOutcome{EventID: "evt-1", Result: audit.CriticalSucceeded}
			ev := w.evaluator(staticGrants{[]authorization.Grant{g}}, w.backend)
			d, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{
				Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
			if err != nil || d.Granted {
				t.Fatalf("malformed grant must not allow: %+v err=%v", d, err)
			}
		})
	}
}

type staticGrants struct{ list []authorization.Grant }

func (s staticGrants) GetGrant(_ context.Context, id string) (authorization.Grant, error) {
	for _, g := range s.list {
		if g.ID == id {
			return g, nil
		}
	}
	return authorization.Grant{}, authorization.ErrNotFound
}

func (s staticGrants) ListGrants(context.Context, identity.PrincipalRef) ([]authorization.Grant, error) {
	return append([]authorization.Grant(nil), s.list...), nil
}

// ---------- границы: ServicePrincipal и delegation ----------

func TestEvaluator_ServiceRequiresDelegationPerTeam(t *testing.T) {
	w := newWorld(t)
	// grant на кластеры двух Teams: сам по себе ничего не разрешает
	g := w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1", "c-b1"), authorization.ConstraintSet{})
	w.wantDenied(svc1, restart, "c-a1", authorization.ReasonDelegationRequired)
	w.wantDenied(svc1, restart, "c-b1", authorization.ReasonDelegationRequired)

	// delegation только от team-a
	dA := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}})
	d := w.wantGranted(svc1, restart, "c-a1")
	if d.DelegationID != dA.ID || d.DelegationRevision != dA.Revision ||
		len(d.GrantIDs) != 1 || d.GrantIDs[0] != g.ID {
		t.Fatalf("decision must carry delegation ID: %+v", d)
	}
	w.wantDenied(svc1, restart, "c-b1", authorization.ReasonDelegationRequired) // team-b не соглашалась

	// delegation team-b открывает c-b1
	dB := w.delegate("team-b", svc1, []authorization.OperationAction{restart}, authorization.TargetScope{OrgID: "org-a", ClusterIDs: []string{"c-b1"}})
	if got := w.wantGranted(svc1, restart, "c-b1"); got.DelegationID != dB.ID {
		t.Fatalf("delegation of the target's owner team must be used: %+v", got)
	}

	// delegation адресована конкретному сервису: svc2 не получает ничего
	w.grant(svc2, []authorization.OperationAction{restart}, org("c-a1", "c-b1"), authorization.ConstraintSet{})
	w.wantDenied(svc2, restart, "c-a1", authorization.ReasonDelegationRequired)
}

func TestEvaluator_DelegationNarrowsButNeverWidens(t *testing.T) {
	w := newWorld(t)
	w.grant(svc1, []authorization.OperationAction{restart, scale}, org("c-a1", "c-a2"), authorization.ConstraintSet{})
	// delegation: только restart и только c-a1
	w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
	w.wantGranted(svc1, restart, "c-a1")
	w.wantDenied(svc1, scale, "c-a1", authorization.ReasonDelegationRequired)   // действие вне delegation
	w.wantDenied(svc1, restart, "c-a2", authorization.ReasonDelegationRequired) // цель вне delegation

	// delegation без grant ничего не даёт
	w2 := newWorld(t)
	w2.delegate("team-a", svc1, []authorization.OperationAction{restart}, authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}})
	w2.wantDenied(svc1, restart, "c-a1", authorization.ReasonGrantRequired)
}

func TestEvaluator_DelegationConstraintsAndLifecycle(t *testing.T) {
	scope := authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}}
	newSvc := func(t *testing.T) *world {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart, del}, org("c-a1", "c-a2"), authorization.ConstraintSet{})
		return w
	}
	t.Run("delegation constraints narrow", func(t *testing.T) {
		w := newSvc(t)
		if _, err := w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{
			Actor: adminA, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: svc1,
			Actions: []authorization.OperationAction{restart, del}, Targets: scope,
			Constraints: authorization.ConstraintSet{AllowedEnvironments: []string{"prod"}, DenyDestructive: true},
			ExpiresAt:   w.future(), MutationID: w.mut(),
		}); err != nil {
			t.Fatal(err)
		}
		w.wantGranted(svc1, restart, "c-a1")
		w.wantDenied(svc1, restart, "c-a2", authorization.ReasonDelegationConstraintNotMet) // staging
		w.wantDenied(svc1, del, "c-a1", authorization.ReasonDelegationConstraintNotMet)     // destructive
	})
	t.Run("suspend resume revoke", func(t *testing.T) {
		w := newSvc(t)
		d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, scope)
		w.wantGranted(svc1, restart, "c-a1")
		sus, err := w.svc.SuspendDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: d.Revision, MutationID: w.mut()})
		if err != nil {
			t.Fatal(err)
		}
		w.wantDenied(svc1, restart, "c-a1", authorization.ReasonDelegationSuspended)
		res, err := w.svc.ResumeDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: sus.Revision, MutationID: w.mut()})
		if err != nil {
			t.Fatal(err)
		}
		w.wantGranted(svc1, restart, "c-a1")
		if _, err := w.svc.RevokeDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: res.Revision, MutationID: w.mut()}); err != nil {
			t.Fatal(err)
		}
		w.wantDenied(svc1, restart, "c-a1", authorization.ReasonDelegationRevoked)
	})
	t.Run("expired", func(t *testing.T) {
		w := newSvc(t)
		d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, scope)
		w.clk.Set(d.ExpiresAt)
		w.wantDenied(svc1, restart, "c-a1", authorization.ReasonDelegationExpired)
	})
	t.Run("another delegation can still qualify", func(t *testing.T) {
		w := newSvc(t)
		old := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, scope)
		fresh := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, scope)
		if _, err := w.svc.RevokeDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: old.ID, ExpectedRevision: old.Revision, MutationID: w.mut()}); err != nil {
			t.Fatal(err)
		}
		if d := w.wantGranted(svc1, restart, "c-a1"); d.DelegationID != fresh.ID {
			t.Fatalf("active delegation must be used: %+v", d)
		}
	})
}

func TestService_DelegationScopeLimitedToOwnerTeam(t *testing.T) {
	w := newWorld(t)
	for name, scope := range map[string]authorization.TargetScope{
		"other team's cluster": org("c-b1"),
		"mixed clusters":       org("c-a1", "c-b1"),
		"other team id":        {OrgID: "org-a", TeamIDs: []string{"team-b"}},
		"foreign org cluster":  org("c-x1"),
		"unknown cluster":      org("c-nope"),
		"namespace of foreign": {OrgID: "org-a", Namespaces: []authorization.NamespaceRef{{ClusterID: "c-b1", Namespace: "ns"}}},
	} {
		_, err := w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{
			Actor: adminA, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: svc1,
			Actions: []authorization.OperationAction{restart}, Targets: scope, ExpiresAt: w.future(), MutationID: w.mut(),
		})
		if !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	// TeamPrincipal не может быть адресатом delegation; сервис другой организации — тоже
	for name, sp := range map[string]identity.PrincipalRef{
		"team principal": tpA,
		"other org":      {Kind: identity.PrincipalService, ID: "sp-9", OrgID: "org-b"},
	} {
		_, err := w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{
			Actor: adminA, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: sp,
			Actions: []authorization.OperationAction{restart}, Targets: org("c-a1"), ExpiresAt: w.future(), MutationID: w.mut(),
		})
		if !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if l, _ := w.backend.ListDelegations(ctx, svc1, "team-a"); len(l) != 0 {
		t.Fatalf("rejected delegations must not be stored")
	}
}

// Organization Admin не может выдать, изменить, приостановить или отозвать delegation за Team.
func TestService_OrgAdminCannotIssueTeamDelegation(t *testing.T) {
	w := newWorld(t)
	scope := authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}}
	_, err := w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{
		Actor: orgAdmin, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: svc1,
		Actions: []authorization.OperationAction{restart}, Targets: scope, ExpiresAt: w.future(), MutationID: w.mut(),
	})
	if !errors.Is(err, authorization.ErrForbidden) {
		t.Fatalf("org admin must not issue Team delegation: %v", err)
	}
	if len(w.rec.callLog()) != 0 {
		t.Fatalf("forbidden request must leave no audit trace: %v", w.rec.callLog())
	}
	// администратор ДРУГОЙ Team — тоже нет
	_, err = w.svc.CreateDelegation(ctx, authorization.CreateDelegationRequest{
		Actor: adminB, OrgID: "org-a", OwnerTeamID: "team-a", ServicePrincipal: svc1,
		Actions: []authorization.OperationAction{restart}, Targets: scope, ExpiresAt: w.future(), MutationID: w.mut(),
	})
	if !errors.Is(err, authorization.ErrForbidden) {
		t.Fatalf("other team's admin must not issue delegation: %v", err)
	}
	// Service проверяет именно AuthorizeDelegationAdmin для Team-владельца и не подменяет проверкой grants.
	for _, c := range w.authz.callLog() {
		if strings.HasPrefix(c, "grant:") {
			t.Fatalf("delegation must never be authorized through grant administration: %v", w.authz.callLog())
		}
	}
	// права на следующие действия тоже у Team Admin, не у Org Admin
	d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, scope)
	for name, fn := range map[string]func() error{
		"suspend": func() error {
			_, e := w.svc.SuspendDelegation(ctx, authorization.DelegationTransitionRequest{Actor: orgAdmin, ID: d.ID, ExpectedRevision: 1, MutationID: w.mut()})
			return e
		},
		"revoke": func() error {
			_, e := w.svc.RevokeDelegation(ctx, authorization.DelegationTransitionRequest{Actor: orgAdmin, ID: d.ID, ExpectedRevision: 1, MutationID: w.mut()})
			return e
		},
		"update": func() error {
			_, e := w.svc.UpdateDelegation(ctx, authorization.UpdateDelegationRequest{Actor: orgAdmin, ID: d.ID, ExpectedRevision: 1,
				Actions: []authorization.OperationAction{scale}, Targets: scope, ExpiresAt: w.future(), MutationID: w.mut()})
			return e
		},
	} {
		if err := fn(); !errors.Is(err, authorization.ErrForbidden) {
			t.Errorf("%s by org admin: want ErrForbidden, got %v", name, err)
		}
	}
	if got, _ := w.backend.GetDelegation(ctx, d.ID); got.Revision != 1 || got.State != authorization.DelegationActive {
		t.Fatalf("delegation changed by an unauthorized actor: %+v", got)
	}
	// Org Admin при этом вправе выдавать grants Service-principal'у своей Organization
	w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
}

func TestService_AdminActorMustBeUser(t *testing.T) {
	w := newWorld(t)
	for _, a := range []identity.ActorRef{
		{Kind: identity.ActorSystem, ID: "sys"}, {Kind: identity.ActorScheduler, ID: "sched"}, {},
	} {
		_, err := w.svc.CreateGrant(ctx, authorization.CreateGrantRequest{
			Actor: a, Principal: tpA, Actions: []authorization.OperationAction{restart}, Targets: org("c-a1"),
			ExpiresAt: w.future(), MutationID: w.mut(),
		})
		if err == nil {
			t.Errorf("actor %+v must not administer grants", a)
		}
	}
	if len(w.rec.callLog()) != 0 {
		t.Fatalf("no audit trace expected: %v", w.rec.callLog())
	}
}

// ---------- fail closed: недоступные/некорректные зависимости ----------

func TestEvaluator_DependencyFailuresFailClosed(t *testing.T) {
	boom := errors.New("secret-cluster-name internals")
	cases := map[string]func(w *world){
		"ownership unavailable": func(w *world) { w.cat.ownErr = boom },
		"actions unavailable":   func(w *world) { w.cat.actErr = boom },
		"principal reader":      func(w *world) { w.ids.err = boom },
		"audit unavailable":     func(w *world) { w.rec.getErr = boom },
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
			w.wantGranted(tpA, restart, "c-a1")
			inject(w)
			d, err := w.eval(tpA, restart, "c-a1")
			if d.Granted || !errors.Is(err, authorization.ErrUnavailable) || d.ReasonCode != authorization.ReasonDependencyUnavailable {
				t.Fatalf("want fail closed, got %+v err=%v", d, err)
			}
			if strings.Contains(err.Error(), "secret-cluster-name") {
				t.Fatalf("dependency error text leaked: %v", err)
			}
		})
	}
	t.Run("grant store unavailable", func(t *testing.T) {
		w := newWorld(t)
		ev := w.evaluator(errGrants{}, w.backend)
		d, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if d.Granted || !errors.Is(err, authorization.ErrUnavailable) {
			t.Fatalf("got %+v err=%v", d, err)
		}
	})
	t.Run("delegation store unavailable", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		ev := w.evaluator(w.backend, errDelegations{})
		d, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if d.Granted || !errors.Is(err, authorization.ErrUnavailable) {
			t.Fatalf("got %+v err=%v", d, err)
		}
	})
	t.Run("zero clock", func(t *testing.T) {
		w := newWorld(t)
		w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		w.clk.Set(time.Time{})
		d, err := w.eval(tpA, restart, "c-a1")
		if d.Granted || !errors.Is(err, authorization.ErrUnavailable) {
			t.Fatalf("got %+v err=%v", d, err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		w := newWorld(t)
		w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		c, cancel := context.WithCancel(ctx)
		cancel()
		d, err := w.ev.EvaluateKubronGrant(c, authorization.GrantRequest{Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if d.Granted || !errors.Is(err, authorization.ErrUnavailable) {
			t.Fatalf("got %+v err=%v", d, err)
		}
	})
}

type errGrants struct{}

func (errGrants) GetGrant(context.Context, string) (authorization.Grant, error) {
	return authorization.Grant{}, errors.New("disk on fire")
}

func (errGrants) ListGrants(context.Context, identity.PrincipalRef) ([]authorization.Grant, error) {
	return nil, errors.New("disk on fire")
}

type errDelegations struct{}

func (errDelegations) GetDelegation(context.Context, string) (authorization.Delegation, error) {
	return authorization.Delegation{}, errors.New("disk on fire")
}

func (errDelegations) ListDelegations(context.Context, identity.PrincipalRef, string) ([]authorization.Delegation, error) {
	return nil, errors.New("disk on fire")
}

func TestEvaluator_UnknownOwnershipAndMalformedFactsDeny(t *testing.T) {
	w := newWorld(t)
	w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	for name, f := range map[string]authorization.TargetFacts{
		"empty team":  {OrgID: "org-a", Environment: "prod"},
		"empty org":   {TeamID: "team-a", Environment: "prod"},
		"bad team id": {OrgID: "org-a", TeamID: "team a", Environment: "prod"},
		"bad env":     {OrgID: "org-a", TeamID: "team-a", Environment: "pro d"},
		"other org":   {OrgID: "org-b", TeamID: "team-a", Environment: "prod"},
	} {
		w.cat.clusters["c-a1"] = f
		d, err := w.eval(tpA, restart, "c-a1")
		if err != nil || d.Granted || d.ReasonCode != authorization.ReasonTargetNotAccessible {
			t.Errorf("%s: got %+v err=%v", name, d, err)
		}
	}
	delete(w.cat.clusters, "c-a1") // неизвестный владелец
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonTargetNotAccessible)
}

func TestEvaluator_UnknownActionAndInvalidRequestDeny(t *testing.T) {
	w := newWorld(t)
	unknown := authorization.OperationAction{OperationID: "nope", ActionID: "nada"}
	w.grant(tpA, []authorization.OperationAction{unknown}, org("c-a1"), authorization.ConstraintSet{})
	w.wantDenied(tpA, unknown, "c-a1", authorization.ReasonActionUnknown) // grant на неизвестное действие ничего не даёт

	for name, req := range map[string]authorization.GrantRequest{
		"bad principal":   {Principal: identity.PrincipalRef{Kind: "x", ID: "y"}, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}},
		"empty operation": {Principal: tpA, ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}},
		"empty action":    {Principal: tpA, OperationID: "workload", Target: authorization.TargetRef{ClusterID: "c-a1"}},
		"empty target":    {Principal: tpA, OperationID: "workload", ActionID: "restart"},
		"name without uid": {Principal: tpA, OperationID: "workload", ActionID: "restart",
			Target: authorization.TargetRef{ClusterID: "c-a1", Version: "v1", Resource: "pods", Namespace: "ns", Name: "p"}},
	} {
		d, err := w.ev.EvaluateKubronGrant(ctx, req)
		if err != nil || d.Granted || d.ReasonCode != authorization.ReasonRequestInvalid {
			t.Errorf("%s: got %+v err=%v", name, d, err)
		}
	}
	// UserPrincipal не участвует в technical grants
	user := identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u-1"}
	d, err := w.eval(user, restart, "c-a1")
	if err != nil || d.Granted || d.ReasonCode != authorization.ReasonPrincipalUnsupported {
		t.Errorf("user principal: got %+v err=%v", d, err)
	}
	// неизвестный principal
	d, err = w.eval(identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ghost", OrgID: "org-a", TeamID: "team-a"}, restart, "c-a1")
	if err != nil || d.Granted || d.ReasonCode != authorization.ReasonPrincipalUnknown {
		t.Errorf("unknown principal: got %+v err=%v", d, err)
	}
}

// ---------- селекторы целей ----------

func TestEvaluator_TargetSelectors(t *testing.T) {
	w := newWorld(t)
	obj := authorization.TargetRef{ClusterID: "c-a1", Group: "apps", Version: "v1", Resource: "deployments", Namespace: "shop", Name: "web", UID: "uid-1"}
	objRef := authorization.ObjectRef(obj)
	check := func(scope authorization.TargetScope, target authorization.TargetRef) bool {
		t.Helper()
		w := newWorld(t)
		w.cat.clusters["c-a1"] = authorization.TargetFacts{OrgID: "org-a", TeamID: "team-a", Environment: "prod"}
		w.grant(tpA, []authorization.OperationAction{restart}, scope, authorization.ConstraintSet{})
		d, err := w.ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: tpA, OperationID: "workload", ActionID: "restart", Target: target})
		if err != nil {
			t.Fatal(err)
		}
		return d.Granted
	}
	_ = w
	ns := authorization.TargetRef{ClusterID: "c-a1", Namespace: "shop"}
	clusterLevel := authorization.TargetRef{ClusterID: "c-a1"}
	otherNs := authorization.TargetRef{ClusterID: "c-a1", Namespace: "other"}
	otherObj := obj
	otherObj.UID = "uid-2"

	type tc struct {
		name   string
		scope  authorization.TargetScope
		target authorization.TargetRef
		want   bool
	}
	for _, c := range []tc{
		{"team subtree covers object", authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"team-a"}}, obj, true},
		{"cluster subtree covers object", org("c-a1"), obj, true},
		{"cluster subtree covers namespace", org("c-a1"), ns, true},
		{"namespace covers its object", authorization.TargetScope{OrgID: "org-a", Namespaces: []authorization.NamespaceRef{{ClusterID: "c-a1", Namespace: "shop"}}}, obj, true},
		{"namespace covers namespace-level", authorization.TargetScope{OrgID: "org-a", Namespaces: []authorization.NamespaceRef{{ClusterID: "c-a1", Namespace: "shop"}}}, ns, true},
		{"namespace does not cover other ns", authorization.TargetScope{OrgID: "org-a", Namespaces: []authorization.NamespaceRef{{ClusterID: "c-a1", Namespace: "shop"}}}, otherNs, false},
		{"namespace does not cover cluster-level", authorization.TargetScope{OrgID: "org-a", Namespaces: []authorization.NamespaceRef{{ClusterID: "c-a1", Namespace: "shop"}}}, clusterLevel, false},
		{"exact object", authorization.TargetScope{OrgID: "org-a", Objects: []authorization.ObjectRef{objRef}}, obj, true},
		{"object with another UID is another object", authorization.TargetScope{OrgID: "org-a", Objects: []authorization.ObjectRef{objRef}}, otherObj, false},
		{"object does not cover its namespace", authorization.TargetScope{OrgID: "org-a", Objects: []authorization.ObjectRef{objRef}}, ns, false},
		{"union: namespace OR object", authorization.TargetScope{OrgID: "org-a",
			Namespaces: []authorization.NamespaceRef{{ClusterID: "c-a1", Namespace: "other"}}, Objects: []authorization.ObjectRef{objRef}}, obj, true},
		{"other cluster of own team", org("c-a2"), obj, false},
	} {
		if got := check(c.scope, c.target); got != c.want {
			t.Errorf("%s: granted=%v want %v", c.name, got, c.want)
		}
	}
}

// ---------- гонки и TOCTOU ----------

func TestEvaluator_RevisionRaceFailsClosed(t *testing.T) {
	t.Run("grant revision changes after listing", func(t *testing.T) {
		w := newWorld(t)
		g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		ev := w.evaluator(afterList{GrantReader: w.backend, fn: func() {
			if _, err := w.svc.UpdateGrant(ctx, authorization.UpdateGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1,
				Actions: []authorization.OperationAction{restart}, Targets: org("c-a1", "c-a2"), ExpiresAt: w.future(), MutationID: w.mut()}); err != nil {
				t.Error(err)
			}
		}}, w.backend)
		d, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || d.Granted || d.ReasonCode != authorization.ReasonStaleRevision {
			t.Fatalf("got %+v err=%v", d, err)
		}
		w.wantGranted(tpA, restart, "c-a1") // следующая оценка видит новую revision
	})
	t.Run("grant revoked after listing", func(t *testing.T) {
		w := newWorld(t)
		g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		ev := w.evaluator(afterList{GrantReader: w.backend, fn: func() {
			if _, err := w.svc.RevokeGrant(ctx, authorization.RevokeGrantRequest{Actor: adminA, ID: g.ID, ExpectedRevision: 1, MutationID: w.mut()}); err != nil {
				t.Error(err)
			}
		}}, w.backend)
		d, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || d.Granted {
			t.Fatalf("revocation race must deny: %+v err=%v", d, err)
		}
	})
	t.Run("grant expires during evaluation", func(t *testing.T) {
		w := newWorld(t)
		g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		ev := w.evaluator(afterList{GrantReader: w.backend, fn: func() { w.clk.Set(g.ExpiresAt) }}, w.backend)
		d, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: tpA, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || d.Granted || d.ReasonCode != authorization.ReasonGrantExpired {
			t.Fatalf("got %+v err=%v", d, err)
		}
	})
	t.Run("delegation revoked after listing", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		fired := false
		ev := w.evaluator(w.backend, afterListDelegations{DelegationReader: w.backend, fn: func() {
			if fired {
				return
			}
			fired = true
			if _, err := w.svc.RevokeDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: 1, MutationID: w.mut()}); err != nil {
				t.Error(err)
			}
		}})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || got.Granted || got.DelegationID != "" || got.DelegationRevision != 0 {
			t.Fatalf("delegation revocation race must deny without permission metadata: %+v err=%v", got, err)
		}
	})
	t.Run("principal revoked during evaluation", func(t *testing.T) {
		w := newWorld(t)
		w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		calls := 0
		w.ids.hook = func() {
			calls++
			if calls == 1 { // между первой проверкой и перепроверкой
				w.ids.mu.Lock()
				w.ids.states[tpA] = identity.IdentityRevoked
				w.ids.mu.Unlock()
			}
		}
		d, err := w.eval(tpA, restart, "c-a1")
		if err != nil || d.Granted {
			t.Fatalf("principal revocation race must deny: %+v err=%v", d, err)
		}
	})

	t.Run("delegation revision changes after listing", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		ev := w.evaluator(w.backend, afterListDelegations{DelegationReader: w.backend, fn: func() {
			if _, err := w.svc.UpdateDelegation(ctx, authorization.UpdateDelegationRequest{
				Actor: adminA, ID: d.ID, ExpectedRevision: d.Revision, Actions: d.Actions, Targets: d.Targets,
				Constraints: d.Constraints, ExpiresAt: d.ExpiresAt, MutationID: w.mut(),
			}); err != nil {
				t.Error(err)
			}
		}})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || got.Granted || got.ReasonCode != authorization.ReasonStaleRevision ||
			got.DelegationID != "" || got.DelegationRevision != 0 {
			t.Fatalf("changed delegation revision must deny without permission metadata: %+v err=%v", got, err)
		}
	})

	t.Run("delegation suspended after listing", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		ev := w.evaluator(w.backend, afterListDelegations{DelegationReader: w.backend, fn: func() {
			if _, err := w.svc.SuspendDelegation(ctx, authorization.DelegationTransitionRequest{Actor: adminA, ID: d.ID, ExpectedRevision: d.Revision, MutationID: w.mut()}); err != nil {
				t.Error(err)
			}
		}})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || got.Granted || got.DelegationID != "" || got.DelegationRevision != 0 {
			t.Fatalf("suspended delegation must deny: %+v err=%v", got, err)
		}
	})

	t.Run("delegation expires during evaluation", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		ev := w.evaluator(w.backend, afterListDelegations{DelegationReader: w.backend, fn: func() { w.clk.Set(d.ExpiresAt) }})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || got.Granted || got.ReasonCode != authorization.ReasonDelegationExpired ||
			got.DelegationID != "" || got.DelegationRevision != 0 {
			t.Fatalf("expired delegation must deny: %+v err=%v", got, err)
		}
	})

	t.Run("unrelated delegation changes do not invalidate selected delegation", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		d1 := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		d2 := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		selected, other := d1, d2
		if other.ID < selected.ID {
			selected, other = other, selected
		}
		ev := w.evaluator(w.backend, afterListDelegations{DelegationReader: w.backend, fn: func() {
			if _, err := w.svc.UpdateDelegation(ctx, authorization.UpdateDelegationRequest{
				Actor: adminA, ID: other.ID, ExpectedRevision: other.Revision, Actions: other.Actions, Targets: other.Targets,
				Constraints: other.Constraints, ExpiresAt: other.ExpiresAt, MutationID: w.mut(),
			}); err != nil {
				t.Error(err)
			}
		}})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || !got.Granted || got.DelegationID != selected.ID || got.DelegationRevision != selected.Revision {
			t.Fatalf("unrelated delegation update must not invalidate selected one: %+v err=%v", got, err)
		}
	})
}

type afterList struct {
	authorization.GrantReader
	fn func()
}

func (a afterList) ListGrants(c context.Context, p identity.PrincipalRef) ([]authorization.Grant, error) {
	l, err := a.GrantReader.ListGrants(c, p)
	a.fn()
	return l, err
}

type afterListDelegations struct {
	authorization.DelegationReader
	fn func()
}

func (a afterListDelegations) ListDelegations(c context.Context, s identity.PrincipalRef, team string) ([]authorization.Delegation, error) {
	l, err := a.DelegationReader.ListDelegations(c, s, team)
	a.fn()
	return l, err
}

// delegationGetHook injects a read failure or mutates a dependency after GetDelegation returns.
type delegationGetHook struct {
	authorization.DelegationReader
	afterGet func(authorization.Delegation)
	err      error
}

func (h delegationGetHook) GetDelegation(ctx context.Context, id string) (authorization.Delegation, error) {
	d, err := h.DelegationReader.GetDelegation(ctx, id)
	if h.afterGet != nil {
		h.afterGet(d)
	}
	if h.err != nil {
		return authorization.Delegation{}, h.err
	}
	return d, err
}

// reverseDelegations proves that selection is based on ID, not backend iteration order.
type reverseDelegations struct{ authorization.DelegationReader }

func (r reverseDelegations) ListDelegations(ctx context.Context, p identity.PrincipalRef, team string) ([]authorization.Delegation, error) {
	list, err := r.DelegationReader.ListDelegations(ctx, p, team)
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, err
}

func TestEvaluator_DelegationDecisionRevisionUsesSelectedRecord(t *testing.T) {
	w := newWorld(t)
	w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	d1 := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
	d2 := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
	selected := d1
	if d2.ID < selected.ID {
		selected = d2
	}
	updated, err := w.svc.UpdateDelegation(ctx, authorization.UpdateDelegationRequest{
		Actor: adminA, ID: selected.ID, ExpectedRevision: selected.Revision, Actions: selected.Actions,
		Targets: selected.Targets, Constraints: selected.Constraints, ExpiresAt: selected.ExpiresAt, MutationID: w.mut(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := w.evaluator(w.backend, reverseDelegations{DelegationReader: w.backend})
	got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
	if err != nil || !got.Granted || got.DelegationID != selected.ID || got.DelegationRevision != updated.Revision {
		t.Fatalf("ID and revision must come from the same deterministically selected record: %+v err=%v; selected=%+v", got, err, updated)
	}
}

func TestEvaluator_DelegationRecheckFailsClosed(t *testing.T) {
	t.Run("backend unavailable on repeated read", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		ev := w.evaluator(w.backend, delegationGetHook{DelegationReader: w.backend, err: authorization.ErrUnavailable})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if !errors.Is(err, authorization.ErrUnavailable) || got.Granted || got.ReasonCode != authorization.ReasonDependencyUnavailable ||
			got.DelegationID != "" || got.DelegationRevision != 0 {
			t.Fatalf("repeated read failure must fail closed with ErrUnavailable: %+v err=%v", got, err)
		}
	})

	t.Run("critical audit outcome changes before final confirmation", func(t *testing.T) {
		w := newWorld(t)
		w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
		w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
		ev := w.evaluator(w.backend, delegationGetHook{DelegationReader: w.backend, afterGet: func(cur authorization.Delegation) {
			w.rec.mu.Lock()
			out := w.rec.outcomes[cur.LastAuditEventID]
			out.Result = audit.CriticalUnknown
			w.rec.outcomes[cur.LastAuditEventID] = out
			w.rec.mu.Unlock()
		}})
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: svc1, OperationID: "workload", ActionID: "restart", Target: authorization.TargetRef{ClusterID: "c-a1"}})
		if err != nil || got.Granted || got.ReasonCode != authorization.ReasonDelegationAuditUnconfirmed ||
			got.DelegationID != "" || got.DelegationRevision != 0 {
			t.Fatalf("unconfirmed final audit outcome must deny: %+v err=%v", got, err)
		}
	})
}

// ---------- критичный аудит и пригодность записей ----------

func TestEvaluator_UnconfirmedAuditDenies(t *testing.T) {
	w := newWorld(t)
	g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w.wantGranted(tpA, restart, "c-a1")

	set := func(res audit.CriticalResult) {
		w.rec.mu.Lock()
		defer w.rec.mu.Unlock()
		out := w.rec.outcomes[g.LastAuditEventID]
		out.Result = res
		if res != audit.CriticalSucceeded {
			out.ReasonCode = "backend_unavailable"
		}
		w.rec.outcomes[g.LastAuditEventID] = out
	}
	set(audit.CriticalUnknown)
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantAuditUnconfirmed)
	set(audit.CriticalFailed)
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantAuditUnconfirmed)
	w.rec.mu.Lock()
	delete(w.rec.outcomes, g.LastAuditEventID) // outcome отсутствует
	w.rec.mu.Unlock()
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantAuditUnconfirmed)
	set(audit.CriticalSucceeded)
	w.rec.mu.Lock()
	w.rec.outcomes[g.LastAuditEventID] = audit.CriticalOutcome{EventID: g.LastAuditEventID, Result: audit.CriticalSucceeded, CompletedAt: t0}
	w.rec.mu.Unlock()
	w.wantGranted(tpA, restart, "c-a1")
}

func TestEvaluator_UnconfirmedDelegationAuditDenies(t *testing.T) {
	w := newWorld(t)
	w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	d := w.delegate("team-a", svc1, []authorization.OperationAction{restart}, org("c-a1"))
	w.wantGranted(svc1, restart, "c-a1")
	w.rec.mu.Lock()
	delete(w.rec.outcomes, d.LastAuditEventID)
	w.rec.mu.Unlock()
	w.wantDenied(svc1, restart, "c-a1", authorization.ReasonDelegationAuditUnconfirmed)
}

func TestEvaluator_AuditOutcomeIsOrganizationScoped(t *testing.T) {
	w := newWorld(t)
	g := w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w.rec.mu.Lock()
	in := w.rec.intents[g.LastAuditEventID]
	in.OrgID = "org-b" // outcome события «чужой» организации не подтверждает наш grant
	w.rec.intents[g.LastAuditEventID] = in
	w.rec.mu.Unlock()
	w.wantDenied(tpA, restart, "c-a1", authorization.ReasonGrantAuditUnconfirmed)
}

// ---------- отсутствие утечек метаданных ----------

func TestEvaluator_NoForeignTargetMetadataLeak(t *testing.T) {
	w := newWorld(t)
	w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	// «расшаренная Target Group» — знание каталога; evaluator его не использует и не раскрывает
	w.cat.shared["c-b1"] = []string{"team-a"}

	foreign := []string{"c-b1" /* чужая Team */, "c-x1" /* чужая Organization */, "c-ghost" /* не существует */}
	var first authorization.GrantDecision
	for i, c := range foreign {
		d, err := w.eval(tpA, restart, c)
		if err != nil || d.Granted || d.ReasonCode != authorization.ReasonTargetNotAccessible {
			t.Fatalf("%s: got %+v err=%v", c, d, err)
		}
		if i == 0 {
			first = d
		} else if !reflect.DeepEqual(d, first) {
			t.Fatalf("decisions for inaccessible targets must be indistinguishable:\n%+v\n%+v", first, d)
		}
		blob := fmt.Sprintf("%+v", d)
		for _, leak := range []string{"team-b", "team-x", "org-b", "prod", "staging"} {
			if strings.Contains(blob, leak) {
				t.Errorf("decision leaks %q: %s", leak, blob)
			}
		}
	}
	// то же для ServicePrincipal: чужая Organization неотличима от несуществующей
	w.grant(svc1, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	d1, _ := w.eval(svc1, restart, "c-x1")
	d2, _ := w.eval(svc1, restart, "c-ghost")
	if d1.ReasonCode != authorization.ReasonTargetNotAccessible || !reflect.DeepEqual(d1, d2) {
		t.Fatalf("service: foreign org must look like missing: %+v vs %+v", d1, d2)
	}
}

// Target Group sharing — не grant: в TargetScope нет селектора групп, а знание каталога о «расшаривании»
// не влияет на решение.
func TestTargetGroupSharingNeverGrants(t *testing.T) {
	typ := reflect.TypeOf(authorization.TargetScope{})
	var fields []string
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i).Name)
	}
	want := []string{"OrgID", "TeamIDs", "ClusterIDs", "Namespaces", "Objects"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("TargetScope must keep explicit selectors only (no groups/labels/scripts): %v", fields)
	}
	w := newWorld(t)
	w.cat.shared["c-b1"] = []string{"team-a", "tp-a", "sp-1"} // группа «расшарена» всем
	w.grant(tpA, []authorization.OperationAction{restart}, org("c-a1"), authorization.ConstraintSet{})
	w.wantDenied(tpA, restart, "c-b1", authorization.ReasonTargetNotAccessible)
	w.wantDenied(svc1, restart, "c-b1", authorization.ReasonDelegationRequired) // без grant и delegation — отказ
}

// ---------- нет закрытых для User kind и «молчаливых» расширений ----------

func TestService_UserPrincipalGetsNoTechnicalGrants(t *testing.T) {
	w := newWorld(t)
	user := identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u-1", OrgID: "org-a"}
	_, err := w.svc.CreateGrant(ctx, authorization.CreateGrantRequest{
		Actor: adminA, Principal: user, Actions: []authorization.OperationAction{restart}, Targets: org("c-a1"),
		ExpiresAt: w.future(), MutationID: w.mut(),
	})
	if !errors.Is(err, authorization.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}
