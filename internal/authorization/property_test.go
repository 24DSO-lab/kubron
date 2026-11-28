// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/authorization"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// Property-тесты evaluator'а: случайные «миры» сверяются с независимым декларативным oracle, а затем
// проверяются инварианты (порядок записей не важен; снятие grant/delegation никогда не расширяет доступ;
// без grants доступа нет; сбой любой зависимости — отказ). Данные генерируются валидными по построению
// (повреждённые записи покрыты отдельными тестами), seed фиксирован — прогон воспроизводим.

var (
	pOps = []authorization.OperationAction{
		{OperationID: "op1", ActionID: "act1"}, {OperationID: "op1", ActionID: "act2"},
		{OperationID: "op2", ActionID: "act1"}, {OperationID: "op2", ActionID: "act2"}, // op2/act2 — destructive
	}
	pUnknown = authorization.OperationAction{OperationID: "op3", ActionID: "act1"}
	pEnvs    = []string{"prod", "staging"}
	pTeams   = []string{"team-a", "team-b"}
)

func pDestructive(a authorization.OperationAction) bool { return a == pOps[3] }

// pTargets — пул целей: кластеры двух Teams своей Organization, чужая Organization, namespace, объекты, неизвестный.
var pTargets = []authorization.TargetRef{
	{ClusterID: "c1"}, {ClusterID: "c2"}, {ClusterID: "c3"}, {ClusterID: "c4"}, {ClusterID: "c5"}, {ClusterID: "c9"},
	{ClusterID: "c1", Namespace: "ns1"}, {ClusterID: "c3", Namespace: "ns2"},
	{ClusterID: "c1", Namespace: "ns1", Group: "apps", Version: "v1", Resource: "deployments", Name: "web", UID: "uid-1"},
	{ClusterID: "c3", Namespace: "ns2", Group: "apps", Version: "v1", Resource: "deployments", Name: "db", UID: "uid-2"},
	{ClusterID: "c1", Namespace: "ns1", Group: "apps", Version: "v1", Resource: "deployments"},
}

var pFacts = map[string]authorization.TargetFacts{
	"c1": {OrgID: "org-a", TeamID: "team-a", Environment: "prod"},
	"c2": {OrgID: "org-a", TeamID: "team-a", Environment: "staging"},
	"c3": {OrgID: "org-a", TeamID: "team-b", Environment: "prod"},
	"c4": {OrgID: "org-a", TeamID: "team-b", Environment: "staging"},
	"c5": {OrgID: "org-b", TeamID: "team-x", Environment: "prod"},
}

type pRecord struct {
	id        string
	owner     string // только для delegation
	actions   []authorization.OperationAction
	scope     authorization.TargetScope
	cons      authorization.ConstraintSet
	revoked   bool
	suspended bool
	expires   time.Time
	audited   bool
	eventID   string
}

type pWorld struct {
	principal   identity.PrincipalRef
	state       identity.IdentityState
	grants      []pRecord
	delegations []pRecord
}

func pick[T any](r *rand.Rand, in []T) T { return in[r.Intn(len(in))] }

func subset[T any](r *rand.Rand, in []T, min int) []T {
	for {
		var out []T
		for _, x := range in {
			if r.Intn(2) == 0 {
				out = append(out, x)
			}
		}
		if len(out) >= min {
			return out
		}
	}
}

func genScope(r *rand.Rand, teams []string) authorization.TargetScope {
	for {
		s := authorization.TargetScope{OrgID: "org-a"}
		if r.Intn(3) == 0 {
			s.TeamIDs = subset(r, teams, 1)
		}
		if r.Intn(2) == 0 {
			s.ClusterIDs = subset(r, []string{"c1", "c2", "c3", "c4", "c5", "c9"}, 1)
		}
		if r.Intn(3) == 0 {
			s.Namespaces = subset(r, []authorization.NamespaceRef{{ClusterID: "c1", Namespace: "ns1"}, {ClusterID: "c3", Namespace: "ns2"}, {ClusterID: "c2", Namespace: "ns1"}}, 1)
		}
		if r.Intn(3) == 0 {
			s.Objects = subset(r, []authorization.ObjectRef{
				authorization.ObjectRef(pTargets[8]), authorization.ObjectRef(pTargets[9]),
				{ClusterID: "c1", Namespace: "ns1", Group: "apps", Version: "v1", Resource: "deployments", Name: "web", UID: "uid-X"},
			}, 1)
		}
		if len(s.TeamIDs)+len(s.ClusterIDs)+len(s.Namespaces)+len(s.Objects) > 0 {
			return s
		}
	}
}

func genRecord(r *rand.Rand, id string, teams []string) pRecord {
	rec := pRecord{id: id, actions: subset(r, pOps, 1), scope: genScope(r, teams), eventID: "evt-" + id}
	if r.Intn(2) == 0 {
		rec.cons.AllowedEnvironments = subset(r, pEnvs, 1)
	}
	rec.cons.DenyDestructive = r.Intn(3) == 0
	rec.expires = t0.Add(time.Hour)
	if r.Intn(6) == 0 {
		rec.expires = t0.Add(-time.Hour)
	}
	if r.Intn(4) == 0 {
		nb := t0.Add(time.Duration(r.Intn(5)-2) * time.Hour)
		if nb.Before(rec.expires) {
			rec.cons.NotBefore = &nb
		}
	}
	rec.revoked = r.Intn(10) == 0
	rec.audited = r.Intn(10) != 0
	return rec
}

func genWorld(r *rand.Rand) pWorld {
	w := pWorld{principal: pick(r, []identity.PrincipalRef{tpA, tpB, svc1}), state: identity.IdentityActive}
	if r.Intn(12) == 0 {
		w.state = pick(r, []identity.IdentityState{identity.IdentitySuspended, identity.IdentityRevoked})
	}
	teams := pTeams
	if w.principal.Kind == identity.PrincipalTeam {
		teams = []string{w.principal.TeamID}
	}
	for i, n := 0, r.Intn(5); i < n; i++ {
		w.grants = append(w.grants, genRecord(r, fmt.Sprintf("g%d", i), teams))
	}
	if w.principal.Kind == identity.PrincipalService {
		for i, n := 0, r.Intn(4); i < n; i++ {
			owner := pick(r, pTeams)
			d := genRecord(r, fmt.Sprintf("d%d", i), []string{owner})
			d.owner = owner
			d.suspended = !d.revoked && r.Intn(8) == 0
			w.delegations = append(w.delegations, d)
		}
	}
	return w
}

// ---------- хранилища-заглушки ----------

func (w pWorld) grantObjs() []authorization.Grant {
	var out []authorization.Grant
	for _, g := range w.grants {
		st := authorization.GrantActive
		if g.revoked {
			st = authorization.GrantRevoked
		}
		out = append(out, authorization.Grant{
			ID: g.id, Principal: w.principal, Actions: g.actions, Targets: g.scope, Constraints: g.cons, State: st, Revision: 1,
			ExpiresAt: g.expires, CreatedBy: adminA, LastMutationID: "mut-" + g.id, LastAuditEventID: g.eventID,
		})
	}
	return out
}

func (w pWorld) delegationObjs() []authorization.Delegation {
	var out []authorization.Delegation
	for _, d := range w.delegations {
		st := authorization.DelegationActive
		switch {
		case d.revoked:
			st = authorization.DelegationRevoked
		case d.suspended:
			st = authorization.DelegationSuspended
		}
		out = append(out, authorization.Delegation{
			ID: d.id, OrgID: "org-a", OwnerTeamID: d.owner, ServicePrincipal: w.principal, Actions: d.actions, Targets: d.scope,
			Constraints: d.cons, State: st, Revision: 1, ExpiresAt: d.expires, CreatedBy: adminA, LastMutationID: "mut-" + d.id,
			LastAuditEventID: d.eventID,
		})
	}
	return out
}

type memGrants struct{ list []authorization.Grant }

func (m memGrants) ListGrants(context.Context, identity.PrincipalRef) ([]authorization.Grant, error) {
	return slices.Clone(m.list), nil
}

func (m memGrants) GetGrant(_ context.Context, id string) (authorization.Grant, error) {
	for _, g := range m.list {
		if g.ID == id {
			return g, nil
		}
	}
	return authorization.Grant{}, authorization.ErrNotFound
}

type memDelegations struct{ list []authorization.Delegation }

func (m memDelegations) ListDelegations(_ context.Context, s identity.PrincipalRef, team string) ([]authorization.Delegation, error) {
	var out []authorization.Delegation
	for _, d := range m.list {
		if d.ServicePrincipal == s && d.OwnerTeamID == team {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m memDelegations) GetDelegation(_ context.Context, id string) (authorization.Delegation, error) {
	for _, d := range m.list {
		if d.ID == id {
			return d, nil
		}
	}
	return authorization.Delegation{}, authorization.ErrNotFound
}

type memOutcomes struct {
	confirmed map[string]bool
	err       error
}

func (m memOutcomes) GetOutcome(_ context.Context, _ string, id string) (audit.CriticalOutcome, error) {
	if m.err != nil {
		return audit.CriticalOutcome{}, m.err
	}
	if m.confirmed[id] {
		return audit.CriticalOutcome{EventID: id, Result: audit.CriticalSucceeded}, nil
	}
	return audit.CriticalOutcome{}, audit.ErrCriticalNotFound
}

// ---------- oracle ----------

type verdictOracle struct {
	granted bool
	grants  []string
	deleg   string
}

func oracleCovers(s authorization.TargetScope, f authorization.TargetFacts, t authorization.TargetRef) bool {
	if slices.Contains(s.TeamIDs, f.TeamID) || slices.Contains(s.ClusterIDs, t.ClusterID) {
		return true
	}
	for _, n := range s.Namespaces {
		if t.Namespace != "" && n.ClusterID == t.ClusterID && n.Namespace == t.Namespace {
			return true
		}
	}
	if t.Name != "" && t.UID != "" {
		for _, o := range s.Objects {
			if o.ClusterID == t.ClusterID && o.Group == t.Group && o.Version == t.Version && o.Resource == t.Resource &&
				o.Namespace == t.Namespace && o.Name == t.Name && o.UID == t.UID {
				return true
			}
		}
	}
	return false
}

func (w pWorld) oracle(a authorization.OperationAction, t authorization.TargetRef) verdictOracle {
	f, ok := pFacts[t.ClusterID]
	if w.state != identity.IdentityActive || !ok || f.OrgID != w.principal.OrgID {
		return verdictOracle{}
	}
	if w.principal.Kind == identity.PrincipalTeam && f.TeamID != w.principal.TeamID {
		return verdictOracle{}
	}
	if !slices.Contains(pOps, a) {
		return verdictOracle{} // неизвестное действие
	}
	ok2 := func(r pRecord) bool {
		if !slices.Contains(r.actions, a) || !oracleCovers(r.scope, f, t) || r.revoked || r.suspended || !t0.Before(r.expires) || !r.audited {
			return false
		}
		if len(r.cons.AllowedEnvironments) > 0 && !slices.Contains(r.cons.AllowedEnvironments, f.Environment) {
			return false
		}
		if r.cons.DenyDestructive && pDestructive(a) {
			return false
		}
		return r.cons.NotBefore == nil || !t0.Before(*r.cons.NotBefore)
	}
	var out verdictOracle
	if w.principal.Kind == identity.PrincipalService {
		for _, d := range w.delegations {
			if d.owner == f.TeamID && ok2(d) && (out.deleg == "" || d.id < out.deleg) {
				out.deleg = d.id
			}
		}
		if out.deleg == "" {
			return verdictOracle{}
		}
	}
	for _, g := range w.grants {
		if ok2(g) {
			out.grants = append(out.grants, g.id)
		}
	}
	sort.Strings(out.grants)
	out.granted = len(out.grants) > 0
	if !out.granted {
		return verdictOracle{}
	}
	return out
}

func (w pWorld) evaluator(t *testing.T, outcomesErr error) *authorization.GrantEvaluator {
	t.Helper()
	conf := map[string]bool{}
	for _, r := range append(slices.Clone(w.grants), w.delegations...) {
		conf[r.eventID] = r.audited
	}
	cat := &catalog{clusters: pFacts, actions: map[authorization.OperationAction]authorization.ActionFacts{}}
	for _, a := range pOps {
		cat.actions[a] = authorization.ActionFacts{Destructive: pDestructive(a)}
	}
	ids := &principals{states: map[identity.PrincipalRef]identity.IdentityState{w.principal: w.state}}
	ev, err := authorization.NewEvaluator(authorization.Deps{
		Grants: memGrants{w.grantObjs()}, Delegations: memDelegations{w.delegationObjs()}, Owners: cat, Principals: ids,
		Actions: cat, Outcomes: memOutcomes{confirmed: conf, err: outcomesErr}, Clock: &clock{t: t0},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func (w pWorld) eval(t *testing.T, a authorization.OperationAction, tr authorization.TargetRef) (authorization.GrantDecision, error) {
	t.Helper()
	return w.evaluator(t, nil).EvaluateKubronGrant(ctx, authorization.GrantRequest{
		Principal: w.principal, OperationID: a.OperationID, ActionID: a.ActionID, Target: tr,
	})
}

const propIterations = 3000

func TestProperty_EvaluatorMatchesOracle(t *testing.T) {
	for _, seed := range []int64{1, 20261127, 777} {
		r := rand.New(rand.NewSource(seed))
		allowed, denied := 0, 0
		for i := 0; i < propIterations; i++ {
			w := genWorld(r)
			a := pick(r, append(slices.Clone(pOps), pUnknown))
			tr := pick(r, pTargets)
			got, err := w.eval(t, a, tr)
			want := w.oracle(a, tr)
			if err != nil {
				t.Fatalf("seed %d #%d: unexpected error %v", seed, i, err)
			}
			if got.Granted != want.granted || !reflect.DeepEqual(nilIfEmpty(got.GrantIDs), nilIfEmpty(want.grants)) || got.DelegationID != want.deleg {
				t.Fatalf("seed %d #%d mismatch\nworld=%+v\nreq=%v %v\n got=%+v\nwant=%+v", seed, i, w, a, tr, got, want)
			}
			if got.Granted != (got.ReasonCode == authorization.ReasonGranted) || (!got.Granted && (len(got.GrantIDs) != 0 || got.DelegationID != "")) {
				t.Fatalf("decision shape: %+v", got)
			}
			if got.Granted {
				allowed++
			} else {
				denied++
			}
		}
		if allowed < propIterations/20 || denied < propIterations/20 {
			t.Fatalf("seed %d: generator is degenerate (allowed=%d denied=%d)", seed, allowed, denied)
		}
	}
}

func nilIfEmpty[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

// Порядок записей в хранилище не влияет на решение.
func TestProperty_OrderIndependence(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < propIterations; i++ {
		w := genWorld(r)
		a, tr := pick(r, pOps), pick(r, pTargets)
		base, err := w.eval(t, a, tr)
		if err != nil {
			t.Fatal(err)
		}
		shuffled := w
		shuffled.grants = slices.Clone(w.grants)
		shuffled.delegations = slices.Clone(w.delegations)
		r.Shuffle(len(shuffled.grants), func(i, j int) { shuffled.grants[i], shuffled.grants[j] = shuffled.grants[j], shuffled.grants[i] })
		r.Shuffle(len(shuffled.delegations), func(i, j int) {
			shuffled.delegations[i], shuffled.delegations[j] = shuffled.delegations[j], shuffled.delegations[i]
		})
		got, err := shuffled.eval(t, a, tr)
		if err != nil || !reflect.DeepEqual(got, base) {
			t.Fatalf("#%d: order changed the decision:\n%+v\n%+v", i, base, got)
		}
	}
}

// Снятие grant/delegation (отзыв, приостановка, удаление) никогда не расширяет доступ; без grants доступа нет.
func TestProperty_RemovalNeverWidens(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < propIterations; i++ {
		w := genWorld(r)
		a, tr := pick(r, pOps), pick(r, pTargets)
		base, err := w.eval(t, a, tr)
		if err != nil {
			t.Fatal(err)
		}
		mutations := map[string]func(pWorld) pWorld{
			"drop all grants": func(x pWorld) pWorld { x.grants = nil; return x },
			"drop all delegations": func(x pWorld) pWorld {
				x.delegations = nil
				return x
			},
			"revoke a grant": func(x pWorld) pWorld {
				if len(x.grants) > 0 {
					x.grants = slices.Clone(x.grants)
					x.grants[r.Intn(len(x.grants))].revoked = true
				}
				return x
			},
			"unconfirm a delegation": func(x pWorld) pWorld {
				if len(x.delegations) > 0 {
					x.delegations = slices.Clone(x.delegations)
					x.delegations[r.Intn(len(x.delegations))].audited = false
				}
				return x
			},
			"principal suspended": func(x pWorld) pWorld { x.state = identity.IdentitySuspended; return x },
		}
		for name, mut := range mutations {
			got, err := mut(w).eval(t, a, tr)
			if err != nil {
				t.Fatal(err)
			}
			if got.Granted && !base.Granted {
				t.Fatalf("#%d %s widened access:\nworld=%+v\nbase=%+v\ngot=%+v", i, name, w, base, got)
			}
		}
		if got, _ := (func(x pWorld) pWorld { x.grants = nil; return x })(w).eval(t, a, tr); got.Granted {
			t.Fatalf("#%d: no grants must never allow: %+v", i, got)
		}
	}
}

// Добавление применимого подтверждённого grant никогда не отнимает доступ (union), а исходный набор
// grants — подмножество результата.
func TestProperty_UnionIsMonotone(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for i := 0; i < propIterations; i++ {
		w := genWorld(r)
		a, tr := pick(r, pOps), pick(r, pTargets)
		base, _ := w.eval(t, a, tr)
		extra := genRecord(r, "gx", func() []string {
			if w.principal.Kind == identity.PrincipalTeam {
				return []string{w.principal.TeamID}
			}
			return pTeams
		}())
		extra.revoked, extra.audited = false, true
		w2 := w
		w2.grants = append(slices.Clone(w.grants), extra)
		got, err := w2.eval(t, a, tr)
		if err != nil {
			t.Fatal(err)
		}
		if base.Granted && !got.Granted {
			t.Fatalf("#%d: adding a grant removed access:\n%+v\n%+v", i, base, got)
		}
		if base.Granted {
			for _, id := range base.GrantIDs {
				if !slices.Contains(got.GrantIDs, id) {
					t.Fatalf("#%d: previously qualifying grant %s disappeared", i, id)
				}
			}
		}
	}
}

// Любая недоступная зависимость превращает решение в отказ с ошибкой ErrUnavailable — никогда не в разрешение.
func TestProperty_DependencyFailureNeverGrants(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	boom := errors.New("boom")
	for i := 0; i < propIterations/3; i++ {
		w := genWorld(r)
		a, tr := pick(r, pOps), pick(r, pTargets)
		base := w.oracle(a, tr)
		if !base.granted {
			continue
		}
		ev := w.evaluator(t, boom) // аудит недоступен
		got, err := ev.EvaluateKubronGrant(ctx, authorization.GrantRequest{Principal: w.principal, OperationID: a.OperationID, ActionID: a.ActionID, Target: tr})
		if got.Granted || !errors.Is(err, authorization.ErrUnavailable) {
			t.Fatalf("#%d: audit outage must fail closed: %+v err=%v", i, got, err)
		}
	}
}
