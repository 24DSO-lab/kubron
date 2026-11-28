// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package authorizationtest — переиспользуемый контрактный набор тестов для реализаций
// authorization.GrantBackend и authorization.DelegationBackend (File backend; PostgreSQL-адаптер интегратора
// подключает тот же набор — Contract Freeze §10).
package authorizationtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/authorization"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// Harness — тестируемые backend'ы и (необязательно) способ «перезапуска».
type Harness struct {
	Grants      authorization.GrantBackend
	Delegations authorization.DelegationBackend
	// Reopen закрывает и заново открывает то же хранилище. nil — кейсы с перезапуском пропускаются.
	Reopen func(t *testing.T) (authorization.GrantBackend, authorization.DelegationBackend)
}

// Factory создаёт чистый Harness для одного теста.
type Factory func(t *testing.T) Harness

var (
	// Base — «текущее» время фикстур; ExpiresAt фикстур — Base+24h.
	Base    = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	counter atomic.Uint64
	actor   = identity.ActorRef{Kind: identity.ActorUser, ID: "admin-1"}
)

const (
	OrgA  = "org-a"
	OrgB  = "org-b"
	TeamA = "team-a"
	TeamB = "team-b"
)

func next(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter.Add(1)) }

// TeamPrincipal и ServicePrincipal строят ссылки технических principal'ов.
func TeamPrincipal(org, team, id string) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: id, OrgID: org, TeamID: team}
}

// ServicePrincipal строит ссылку ServicePrincipal.
func ServicePrincipal(org, id string) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalService, ID: id, OrgID: org}
}

// NewGrant строит валидный grant на создание (Revision 0) для principal: одна пара действий, один кластер.
func NewGrant(p identity.PrincipalRef) authorization.Grant {
	return authorization.Grant{
		ID: next("grt"), Principal: p,
		Actions:   []authorization.OperationAction{{OperationID: "workload", ActionID: "restart"}},
		Targets:   authorization.TargetScope{OrgID: p.OrgID, ClusterIDs: []string{"c-1"}},
		State:     authorization.GrantActive,
		ExpiresAt: Base.Add(24 * time.Hour), CreatedBy: actor,
		LastMutationID: next("mut"), LastAuditEventID: next("evt"),
	}
}

// NewDelegation строит валидную delegation на создание от team для service.
func NewDelegation(service identity.PrincipalRef, team string) authorization.Delegation {
	return authorization.Delegation{
		ID: next("dlg"), OrgID: service.OrgID, OwnerTeamID: team, ServicePrincipal: service,
		Actions:   []authorization.OperationAction{{OperationID: "workload", ActionID: "restart"}},
		Targets:   authorization.TargetScope{OrgID: service.OrgID, TeamIDs: []string{team}},
		State:     authorization.DelegationActive,
		ExpiresAt: Base.Add(24 * time.Hour), CreatedBy: actor,
		LastMutationID: next("mut"), LastAuditEventID: next("evt"),
	}
}

func is(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: want %v, got %v", what, want, err)
	}
}

// Run выполняет контрактный набор.
func Run(t *testing.T, newHarness Factory) {
	sub := func(name string, fn func(t *testing.T, h Harness, c chk)) {
		t.Run(name, func(t *testing.T) { fn(t, newHarness(t), chk{t}) })
	}
	ctx := context.Background()

	// ================= grants =================

	sub("grant/create_get_canonical", func(t *testing.T, h Harness, c chk) {
		p := TeamPrincipal(OrgA, TeamA, "tp-1")
		in := NewGrant(p)
		in.Actions = []authorization.OperationAction{{OperationID: "b", ActionID: "y"}, {OperationID: "a", ActionID: "x"}, {OperationID: "b", ActionID: "y"}}
		in.Targets.ClusterIDs = []string{"c-2", "c-1", "c-2"}
		in.Targets.TeamIDs = []string{TeamA, TeamA}
		in.Constraints.AllowedEnvironments = []string{"prod", "dev", "prod"}
		got := c.G(h.Grants.CreateGrant(ctx, in))
		if got.Revision != 1 || got.State != authorization.GrantActive {
			t.Fatalf("revision/state: %+v", got)
		}
		if len(got.Actions) != 2 || got.Actions[0].OperationID != "a" || got.Actions[1].OperationID != "b" {
			t.Errorf("actions not canonical: %+v", got.Actions)
		}
		if strings.Join(got.Targets.ClusterIDs, ",") != "c-1,c-2" || strings.Join(got.Constraints.AllowedEnvironments, ",") != "dev,prod" ||
			len(got.Targets.TeamIDs) != 1 {
			t.Errorf("lists not canonical: %+v %+v", got.Targets, got.Constraints)
		}
		read := c.G(h.Grants.GetGrant(ctx, in.ID))
		if read.ID != in.ID || read.Revision != 1 || !read.ExpiresAt.Equal(in.ExpiresAt) || read.LastAuditEventID != in.LastAuditEventID {
			t.Errorf("get mismatch: %+v", read)
		}
		_, err := h.Grants.GetGrant(ctx, "missing")
		is(t, "get missing", err, authorization.ErrNotFound)
	})

	sub("grant/validation", func(t *testing.T, h Harness, c chk) {
		p := TeamPrincipal(OrgA, TeamA, "tp-1")
		nb := Base.Add(48 * time.Hour)
		cases := map[string]func(*authorization.Grant){
			"empty id": func(g *authorization.Grant) { g.ID = "" },
			"bad id":   func(g *authorization.Grant) { g.ID = "a/b" },
			"user principal": func(g *authorization.Grant) {
				g.Principal = identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "u"}
			},
			"no actions":         func(g *authorization.Grant) { g.Actions = nil },
			"empty pair":         func(g *authorization.Grant) { g.Actions = []authorization.OperationAction{{OperationID: "a"}} },
			"org only scope":     func(g *authorization.Grant) { g.Targets = authorization.TargetScope{OrgID: OrgA} },
			"empty scope":        func(g *authorization.Grant) { g.Targets = authorization.TargetScope{} },
			"scope org mismatch": func(g *authorization.Grant) { g.Targets.OrgID = OrgB },
			"foreign team":       func(g *authorization.Grant) { g.Targets.TeamIDs = []string{TeamB} },
			"own+foreign team":   func(g *authorization.Grant) { g.Targets.TeamIDs = []string{TeamA, TeamB} },
			"no expiry":          func(g *authorization.Grant) { g.ExpiresAt = time.Time{} },
			"revision set":       func(g *authorization.Grant) { g.Revision = 1 },
			"revoked on create":  func(g *authorization.Grant) { g.State = authorization.GrantRevoked },
			"unknown state":      func(g *authorization.Grant) { g.State = "bogus" },
			"no mutation id":     func(g *authorization.Grant) { g.LastMutationID = "" },
			"no audit event":     func(g *authorization.Grant) { g.LastAuditEventID = "" },
			"bad creator":        func(g *authorization.Grant) { g.CreatedBy = identity.ActorRef{Kind: "robot", ID: "x"} },
			"object without uid": func(g *authorization.Grant) {
				g.Targets.Objects = []authorization.ObjectRef{{ClusterID: "c-1", Version: "v1", Resource: "pods", Name: "p"}}
			},
			"bad namespace": func(g *authorization.Grant) {
				g.Targets.Namespaces = []authorization.NamespaceRef{{ClusterID: "c-1", Namespace: "a/b"}}
			},
			"not before >= expiry": func(g *authorization.Grant) { g.Constraints.NotBefore = &nb },
			"bad environment":      func(g *authorization.Grant) { g.Constraints.AllowedEnvironments = []string{"pro d"} },
		}
		for name, mut := range cases {
			g := NewGrant(p)
			mut(&g)
			if _, err := h.Grants.CreateGrant(ctx, g); !errors.Is(err, authorization.ErrInvalid) {
				t.Errorf("%s: want ErrInvalid, got %v", name, err)
			}
		}
		if l := c.L(h.Grants.ListGrants(ctx, p)); len(l) != 0 {
			t.Fatalf("invalid grants must not be stored: %d", len(l))
		}
		// сервисный principal: TeamIDs любых команд допустимы (границы задаёт delegation)
		sp := ServicePrincipal(OrgA, "sp-1")
		g := NewGrant(sp)
		g.Targets.TeamIDs = []string{TeamA, TeamB}
		if _, err := h.Grants.CreateGrant(ctx, g); err != nil {
			t.Errorf("service grant with several teams: %v", err)
		}
	})

	sub("grant/duplicate_and_replay", func(t *testing.T, h Harness, c chk) {
		in := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		first := c.G(h.Grants.CreateGrant(ctx, in))
		again := c.G(h.Grants.CreateGrant(ctx, in))
		if again.Revision != 1 || again.LastAuditEventID != first.LastAuditEventID {
			t.Errorf("replay must be idempotent: %+v", again)
		}
		other := in
		other.LastMutationID = next("mut")
		_, err := h.Grants.CreateGrant(ctx, other)
		is(t, "duplicate create with other mutation", err, authorization.ErrConflict)
		diff := in
		diff.Actions = []authorization.OperationAction{{OperationID: "x", ActionID: "y"}}
		_, err = h.Grants.CreateGrant(ctx, diff)
		is(t, "same mutation different content", err, authorization.ErrConflict)
		if l := c.L(h.Grants.ListGrants(ctx, in.Principal)); len(l) != 1 {
			t.Fatalf("want 1 grant, got %d", len(l))
		}
	})

	sub("grant/list_isolation", func(t *testing.T, h Harness, c chk) {
		a := TeamPrincipal(OrgA, TeamA, "p-1")
		sameIDService := ServicePrincipal(OrgA, "p-1")
		otherOrg := TeamPrincipal(OrgB, TeamA, "p-1")
		otherTeam := TeamPrincipal(OrgA, TeamB, "p-1")
		for _, p := range []identity.PrincipalRef{a, sameIDService, otherOrg, otherTeam} {
			c.G(h.Grants.CreateGrant(ctx, NewGrant(p)))
		}
		for _, p := range []identity.PrincipalRef{a, sameIDService, otherOrg, otherTeam} {
			l := c.L(h.Grants.ListGrants(ctx, p))
			if len(l) != 1 || l[0].Principal != p {
				t.Errorf("principal %+v must see only own grants, got %+v", p, l)
			}
		}
		_, err := h.Grants.ListGrants(ctx, identity.PrincipalRef{Kind: "x", ID: "y"})
		is(t, "list invalid principal", err, authorization.ErrInvalid)
	})

	sub("grant/no_aliasing", func(t *testing.T, h Harness, c chk) {
		in := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		in.Targets.ClusterIDs = []string{"c-1", "c-2"}
		got := c.G(h.Grants.CreateGrant(ctx, in))
		in.Targets.ClusterIDs[0] = "tampered-in"
		got.Targets.ClusterIDs[0] = "tampered-out"
		got.Actions[0].ActionID = "tampered-out"
		read := c.G(h.Grants.GetGrant(ctx, in.ID))
		if read.Targets.ClusterIDs[0] != "c-1" || read.Actions[0].ActionID != "restart" {
			t.Fatalf("stored state must not alias caller memory: %+v", read)
		}
	})

	sub("grant/update_cas", func(t *testing.T, h Harness, c chk) {
		in := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		cur := c.G(h.Grants.CreateGrant(ctx, in))
		upd := cur
		upd.Actions = []authorization.OperationAction{{OperationID: "workload", ActionID: "scale"}}
		upd.LastMutationID, upd.LastAuditEventID = next("mut"), next("evt")
		upd.Revision = 0

		_, err := h.Grants.UpdateGrant(ctx, upd, 0)
		is(t, "zero is not a wildcard", err, authorization.ErrInvalid)
		_, err = h.Grants.UpdateGrant(ctx, upd, 7)
		is(t, "stale revision", err, authorization.ErrConflict)
		wrongRev := upd
		wrongRev.Revision = 5
		_, err = h.Grants.UpdateGrant(ctx, wrongRev, 1)
		is(t, "in.Revision contradicting expected", err, authorization.ErrInvalid)

		got := c.G(h.Grants.UpdateGrant(ctx, upd, 1))
		if got.Revision != 2 || got.Actions[0].ActionID != "scale" || got.LastAuditEventID != upd.LastAuditEventID {
			t.Fatalf("update result: %+v", got)
		}
		again := c.G(h.Grants.UpdateGrant(ctx, upd, 1))
		if again.Revision != 2 {
			t.Errorf("idempotent replay changed revision: %d", again.Revision)
		}
		diff := upd
		diff.Actions = []authorization.OperationAction{{OperationID: "workload", ActionID: "delete"}}
		_, err = h.Grants.UpdateGrant(ctx, diff, 1)
		is(t, "same mutation different content", err, authorization.ErrConflict)

		imm := upd
		imm.LastMutationID, imm.LastAuditEventID = next("mut"), next("evt")
		imm.Principal = TeamPrincipal(OrgA, TeamA, "tp-other")
		_, err = h.Grants.UpdateGrant(ctx, imm, 2)
		is(t, "principal is immutable", err, authorization.ErrInvalid)
		imm = upd
		imm.LastMutationID, imm.LastAuditEventID = next("mut"), next("evt")
		imm.CreatedBy = identity.ActorRef{Kind: identity.ActorUser, ID: "someone-else"}
		_, err = h.Grants.UpdateGrant(ctx, imm, 2)
		is(t, "creator is immutable", err, authorization.ErrInvalid)
		missing := upd
		missing.ID = "grt-missing"
		missing.LastMutationID = next("mut")
		_, err = h.Grants.UpdateGrant(ctx, missing, 1)
		is(t, "update missing", err, authorization.ErrNotFound)
		if r := c.G(h.Grants.GetGrant(ctx, in.ID)); r.Revision != 2 {
			t.Errorf("failed updates must not change state: %+v", r)
		}
	})

	sub("grant/revoke_terminal", func(t *testing.T, h Harness, c chk) {
		in := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		cur := c.G(h.Grants.CreateGrant(ctx, in))
		mut, evt := next("mut"), next("evt")
		_, err := h.Grants.RevokeGrant(ctx, in.ID, 0, mut, evt)
		is(t, "zero revision", err, authorization.ErrInvalid)
		_, err = h.Grants.RevokeGrant(ctx, in.ID, 9, mut, evt)
		is(t, "stale revision", err, authorization.ErrConflict)
		_, err = h.Grants.RevokeGrant(ctx, "missing", 1, mut, evt)
		is(t, "missing", err, authorization.ErrNotFound)
		_, err = h.Grants.RevokeGrant(ctx, in.ID, 1, "bad id", evt)
		is(t, "bad mutation id", err, authorization.ErrInvalid)

		got := c.G(h.Grants.RevokeGrant(ctx, in.ID, cur.Revision, mut, evt))
		if got.State != authorization.GrantRevoked || got.Revision != 2 || got.LastAuditEventID != evt {
			t.Fatalf("revoke result: %+v", got)
		}
		again := c.G(h.Grants.RevokeGrant(ctx, in.ID, 1, mut, evt))
		if again.Revision != 2 {
			t.Errorf("idempotent revoke changed revision")
		}
		_, err = h.Grants.RevokeGrant(ctx, in.ID, 2, next("mut"), next("evt"))
		is(t, "revoke revoked", err, authorization.ErrRevoked)
		upd := got
		upd.State, upd.Revision = authorization.GrantActive, 0
		upd.LastMutationID, upd.LastAuditEventID = next("mut"), next("evt")
		_, err = h.Grants.UpdateGrant(ctx, upd, 2)
		is(t, "revoked cannot be revived", err, authorization.ErrRevoked)
		if r := c.G(h.Grants.GetGrant(ctx, in.ID)); r.State != authorization.GrantRevoked {
			t.Fatalf("revoked grant was revived: %+v", r)
		}
	})

	sub("grant/concurrent_update", func(t *testing.T, h Harness, c chk) {
		in := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		cur := c.G(h.Grants.CreateGrant(ctx, in))
		const n = 16
		var wg sync.WaitGroup
		var ok, conflicts atomic.Int32
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				u := cur
				u.Revision = 0
				u.Actions = []authorization.OperationAction{{OperationID: "workload", ActionID: fmt.Sprintf("act-%d", i)}}
				u.LastMutationID, u.LastAuditEventID = next("mut"), next("evt")
				_, err := h.Grants.UpdateGrant(ctx, u, 1)
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, authorization.ErrConflict):
					conflicts.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}(i)
		}
		wg.Wait()
		if ok.Load() != 1 || conflicts.Load() != n-1 {
			t.Fatalf("CAS must admit exactly one writer: ok=%d conflicts=%d", ok.Load(), conflicts.Load())
		}
		if r := c.G(h.Grants.GetGrant(ctx, in.ID)); r.Revision != 2 {
			t.Fatalf("revision = %d, want 2", r.Revision)
		}
	})

	sub("grant/persists_across_reopen", func(t *testing.T, h Harness, c chk) {
		if h.Reopen == nil {
			t.Skip("backend has no reopen")
		}
		in := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		in.Constraints = authorization.ConstraintSet{AllowedEnvironments: []string{"prod"}, DenyDestructive: true, NotBefore: &Base}
		c.G(h.Grants.CreateGrant(ctx, in))
		c.G(h.Grants.RevokeGrant(ctx, in.ID, 1, next("mut"), next("evt")))
		in2 := NewGrant(TeamPrincipal(OrgA, TeamA, "tp-1"))
		in2.Constraints = in.Constraints
		c.G(h.Grants.CreateGrant(ctx, in2))
		g2, _ := h.Reopen(t)
		r1 := c.G(g2.GetGrant(ctx, in.ID))
		if r1.State != authorization.GrantRevoked || r1.Revision != 2 {
			t.Errorf("revoked state lost: %+v", r1)
		}
		r2 := c.G(g2.GetGrant(ctx, in2.ID))
		cs := r2.Constraints
		if !cs.DenyDestructive || len(cs.AllowedEnvironments) != 1 || cs.NotBefore == nil || !cs.NotBefore.Equal(Base) {
			t.Errorf("constraints lost: %+v", cs)
		}
		l := c.L(g2.ListGrants(ctx, in.Principal))
		if len(l) != 2 {
			t.Errorf("want 2 grants after reopen, got %d", len(l))
		}
	})

	// ================= delegations =================

	sub("delegation/create_get_list", func(t *testing.T, h Harness, c chk) {
		sp, sp2 := ServicePrincipal(OrgA, "sp-1"), ServicePrincipal(OrgA, "sp-2")
		a := c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(sp, TeamA)))
		c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(sp, TeamB)))
		c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(sp2, TeamA)))
		c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(ServicePrincipal(OrgB, "sp-1"), TeamA)))
		if a.Revision != 1 || a.State != authorization.DelegationActive {
			t.Fatalf("create result: %+v", a)
		}
		l := c.DL(h.Delegations.ListDelegations(ctx, sp, TeamA))
		if len(l) != 1 || l[0].ID != a.ID {
			t.Fatalf("list must return only this service from this team: %+v", l)
		}
		_, err := h.Delegations.ListDelegations(ctx, sp, "")
		is(t, "empty team is not a wildcard", err, authorization.ErrInvalid)
		_, err = h.Delegations.ListDelegations(ctx, TeamPrincipal(OrgA, TeamA, "tp"), TeamA)
		is(t, "team principal cannot hold delegations", err, authorization.ErrInvalid)
		_, err = h.Delegations.GetDelegation(ctx, "missing")
		is(t, "get missing", err, authorization.ErrNotFound)
	})

	sub("delegation/validation", func(t *testing.T, h Harness, c chk) {
		sp := ServicePrincipal(OrgA, "sp-1")
		cases := map[string]func(*authorization.Delegation){
			"team principal":      func(d *authorization.Delegation) { d.ServicePrincipal = TeamPrincipal(OrgA, TeamA, "tp") },
			"foreign org service": func(d *authorization.Delegation) { d.ServicePrincipal = ServicePrincipal(OrgB, "sp-1") },
			"empty owner team":    func(d *authorization.Delegation) { d.OwnerTeamID = "" },
			"empty org":           func(d *authorization.Delegation) { d.OrgID = "" },
			"scope org mismatch":  func(d *authorization.Delegation) { d.Targets.OrgID = OrgB },
			"scope other team":    func(d *authorization.Delegation) { d.Targets.TeamIDs = []string{TeamA, TeamB} },
			"empty scope":         func(d *authorization.Delegation) { d.Targets = authorization.TargetScope{OrgID: OrgA} },
			"no actions":          func(d *authorization.Delegation) { d.Actions = nil },
			"no expiry":           func(d *authorization.Delegation) { d.ExpiresAt = time.Time{} },
			"suspended on create": func(d *authorization.Delegation) { d.State = authorization.DelegationSuspended },
			"revision set":        func(d *authorization.Delegation) { d.Revision = 3 },
			"no audit event":      func(d *authorization.Delegation) { d.LastAuditEventID = "" },
		}
		for name, mut := range cases {
			d := NewDelegation(sp, TeamA)
			mut(&d)
			if _, err := h.Delegations.CreateDelegation(ctx, d); !errors.Is(err, authorization.ErrInvalid) {
				t.Errorf("%s: want ErrInvalid, got %v", name, err)
			}
		}
		if l := c.DL(h.Delegations.ListDelegations(ctx, sp, TeamA)); len(l) != 0 {
			t.Fatalf("invalid delegations must not be stored")
		}
	})

	sub("delegation/duplicate_and_replay", func(t *testing.T, h Harness, c chk) {
		in := NewDelegation(ServicePrincipal(OrgA, "sp-1"), TeamA)
		first := c.D(h.Delegations.CreateDelegation(ctx, in))
		again := c.D(h.Delegations.CreateDelegation(ctx, in))
		if again.Revision != 1 || again.LastAuditEventID != first.LastAuditEventID {
			t.Errorf("replay must be idempotent: %+v", again)
		}
		other := in
		other.LastMutationID = next("mut")
		_, err := h.Delegations.CreateDelegation(ctx, other)
		is(t, "duplicate", err, authorization.ErrConflict)
	})

	sub("delegation/state_machine", func(t *testing.T, h Harness, c chk) {
		in := NewDelegation(ServicePrincipal(OrgA, "sp-1"), TeamA)
		cur := c.D(h.Delegations.CreateDelegation(ctx, in))
		step := func(from authorization.Delegation, to authorization.DelegationState) (authorization.Delegation, error) {
			u := from
			u.State, u.Revision = to, 0
			u.LastMutationID, u.LastAuditEventID = next("mut"), next("evt")
			return h.Delegations.UpdateDelegation(ctx, u, from.Revision)
		}
		sus := c.D(step(cur, authorization.DelegationSuspended))
		if sus.State != authorization.DelegationSuspended || sus.Revision != 2 {
			t.Fatalf("suspend: %+v", sus)
		}
		res := c.D(step(sus, authorization.DelegationActive))
		if res.State != authorization.DelegationActive || res.Revision != 3 {
			t.Fatalf("resume: %+v", res)
		}
		_, err := step(res, authorization.DelegationRevoked)
		is(t, "revoke only through RevokeDelegation", err, authorization.ErrInvalid)
		_, err = step(res, "bogus")
		is(t, "unknown state", err, authorization.ErrInvalid)

		_, err = h.Delegations.RevokeDelegation(ctx, in.ID, 0, next("mut"), next("evt"))
		is(t, "zero revision", err, authorization.ErrInvalid)
		_, err = h.Delegations.RevokeDelegation(ctx, in.ID, 1, next("mut"), next("evt"))
		is(t, "stale revision", err, authorization.ErrConflict)
		mut, evt := next("mut"), next("evt")
		rev := c.D(h.Delegations.RevokeDelegation(ctx, in.ID, 3, mut, evt))
		if rev.State != authorization.DelegationRevoked || rev.Revision != 4 || rev.LastAuditEventID != evt {
			t.Fatalf("revoke: %+v", rev)
		}
		if again := c.D(h.Delegations.RevokeDelegation(ctx, in.ID, 3, mut, evt)); again.Revision != 4 {
			t.Errorf("idempotent revoke changed revision")
		}
		_, err = step(rev, authorization.DelegationActive)
		is(t, "revoked cannot be revived", err, authorization.ErrRevoked)
		_, err = h.Delegations.RevokeDelegation(ctx, in.ID, 4, next("mut"), next("evt"))
		is(t, "revoke revoked", err, authorization.ErrRevoked)
		_, err = h.Delegations.RevokeDelegation(ctx, "missing", 1, next("mut"), next("evt"))
		is(t, "revoke missing", err, authorization.ErrNotFound)
	})

	sub("delegation/immutable_fields", func(t *testing.T, h Harness, c chk) {
		in := NewDelegation(ServicePrincipal(OrgA, "sp-1"), TeamA)
		cur := c.D(h.Delegations.CreateDelegation(ctx, in))
		for name, mut := range map[string]func(*authorization.Delegation){
			"owner team": func(d *authorization.Delegation) { d.OwnerTeamID = TeamB; d.Targets.TeamIDs = []string{TeamB} },
			"service":    func(d *authorization.Delegation) { d.ServicePrincipal = ServicePrincipal(OrgA, "sp-2") },
			"creator": func(d *authorization.Delegation) {
				d.CreatedBy = identity.ActorRef{Kind: identity.ActorUser, ID: "other"}
			},
		} {
			u := cur
			u.Revision = 0
			u.LastMutationID, u.LastAuditEventID = next("mut"), next("evt")
			mut(&u)
			if _, err := h.Delegations.UpdateDelegation(ctx, u, 1); !errors.Is(err, authorization.ErrInvalid) {
				t.Errorf("%s: want ErrInvalid, got %v", name, err)
			}
		}
		// содержимое изменяемо; CAS и идемпотентность
		u := cur
		u.Revision = 0
		u.Actions = []authorization.OperationAction{{OperationID: "workload", ActionID: "scale"}}
		u.LastMutationID, u.LastAuditEventID = next("mut"), next("evt")
		got := c.D(h.Delegations.UpdateDelegation(ctx, u, 1))
		if got.Revision != 2 || got.Actions[0].ActionID != "scale" {
			t.Fatalf("update: %+v", got)
		}
		if again := c.D(h.Delegations.UpdateDelegation(ctx, u, 1)); again.Revision != 2 {
			t.Errorf("idempotent replay changed revision")
		}
		_, err := h.Delegations.UpdateDelegation(ctx, u, 5)
		is(t, "stale", err, authorization.ErrConflict)
		_, err = h.Delegations.UpdateDelegation(ctx, u, 0)
		is(t, "zero", err, authorization.ErrInvalid)
	})

	sub("delegation/concurrent_update", func(t *testing.T, h Harness, c chk) {
		cur := c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(ServicePrincipal(OrgA, "sp-1"), TeamA)))
		const n = 16
		var wg sync.WaitGroup
		var ok, conflicts atomic.Int32
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				u := cur
				u.Revision = 0
				u.Actions = []authorization.OperationAction{{OperationID: "workload", ActionID: fmt.Sprintf("act-%d", i)}}
				u.LastMutationID, u.LastAuditEventID = next("mut"), next("evt")
				_, err := h.Delegations.UpdateDelegation(ctx, u, 1)
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, authorization.ErrConflict):
					conflicts.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}(i)
		}
		wg.Wait()
		if ok.Load() != 1 || conflicts.Load() != n-1 {
			t.Fatalf("CAS must admit exactly one writer: ok=%d conflicts=%d", ok.Load(), conflicts.Load())
		}
	})

	sub("delegation/persists_across_reopen", func(t *testing.T, h Harness, c chk) {
		if h.Reopen == nil {
			t.Skip("backend has no reopen")
		}
		sp := ServicePrincipal(OrgA, "sp-1")
		d := c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(sp, TeamA)))
		c.D(h.Delegations.RevokeDelegation(ctx, d.ID, 1, next("mut"), next("evt")))
		d2 := c.D(h.Delegations.CreateDelegation(ctx, NewDelegation(sp, TeamA)))
		_, dels := h.Reopen(t)
		l := c.DL(dels.ListDelegations(ctx, sp, TeamA))
		if len(l) != 2 {
			t.Fatalf("want 2 delegations after reopen, got %d", len(l))
		}
		r := c.D(dels.GetDelegation(ctx, d.ID))
		if r.State != authorization.DelegationRevoked {
			t.Errorf("revoked state lost: %+v", r)
		}
		if r2 := c.D(dels.GetDelegation(ctx, d2.ID)); r2.State != authorization.DelegationActive {
			t.Errorf("active state lost: %+v", r2)
		}
	})
}

// chk — проверки текущего подтеста: любая неожиданная ошибка прерывает его.
type chk struct{ t *testing.T }

func (c chk) fail(err error) {
	c.t.Helper()
	if err != nil {
		c.t.Fatalf("unexpected error: %v", err)
	}
}

func (c chk) G(g authorization.Grant, err error) authorization.Grant {
	c.t.Helper()
	c.fail(err)
	return g
}
func (c chk) L(g []authorization.Grant, err error) []authorization.Grant {
	c.t.Helper()
	c.fail(err)
	return g
}
func (c chk) D(d authorization.Delegation, err error) authorization.Delegation {
	c.t.Helper()
	c.fail(err)
	return d
}
func (c chk) DL(d []authorization.Delegation, err error) []authorization.Delegation {
	c.t.Helper()
	c.fail(err)
	return d
}
