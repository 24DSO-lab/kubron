// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// principalBroker records the subjects it is asked for and can be scripted to refuse.
type principalBroker struct {
	scriptBroker
	mu       sync.Mutex
	subjects []credential.Subject
	asked    int
}

func (b *principalBroker) Token(ctx context.Context, s credential.Subject, d credential.Descriptor) (credential.Token, error) {
	b.mu.Lock()
	b.subjects = append(b.subjects, s)
	b.mu.Unlock()
	return b.scriptBroker.Token(ctx, s, d)
}

func teamRef(org, team, id string) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: id, OrgID: org, TeamID: team}
}

func serviceRef(org, id string) identity.PrincipalRef {
	return identity.PrincipalRef{Kind: identity.PrincipalService, ID: id, OrgID: org}
}

// technicalCluster stores a cluster owned by team t1 whose sign-in is an exec plugin (broker-managed).
func technicalCluster(t *testing.T) string {
	t.Helper()
	memStoreWithHome(t)
	meta, err := AddClusterFromTextToTeam(execKubeconfig, ClusterSettings{DisplayName: "tech"}, "t1")
	if err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

// A technical plan is built for a Team/Service principal only; every other shape is refused without a fallback.
func TestPlanForPrincipalRejectsInvalidInput(t *testing.T) {
	id := technicalCluster(t)
	b := &principalBroker{scriptBroker: scriptBroker{kind: credential.KindOIDC}}
	team := teamRef("o1", "t1", "ti-1")

	ok, err := PlanForPrincipal(b, team, "bnd-1", id)
	if err != nil {
		t.Fatalf("a valid plan must be built: %v", err)
	}
	if ok.Subject.Scope != credential.ScopePrincipal || ok.Subject.Principal != team || ok.Subject.BindingID != "bnd-1" ||
		ok.Subject.OwnerID != "" || ok.Subject.SessionID != "" {
		t.Fatalf("the subject must be exactly the technical one: %+v", ok.Subject)
	}

	// A cluster whose sign-in is static, in the same store (technicalCluster reset it above).
	staticMeta, err := AddClusterFromTextToTeam(sampleKubeconfig, ClusterSettings{DisplayName: "static"}, "t1")
	if err != nil {
		t.Fatal(err)
	}
	staticID := staticMeta.ID
	staticBroker := &principalBroker{scriptBroker: scriptBroker{kind: credential.KindStatic}}

	tests := []struct {
		name      string
		b         credential.Broker
		principal identity.PrincipalRef
		binding   string
		cluster   string
	}{
		{"user principal", b, identity.PrincipalRef{Kind: identity.PrincipalUser, ID: "alice"}, "bnd-1", id},
		{"missing binding", b, team, "", id},
		{"invalid binding id", b, team, "bad id/with slash", id},
		{"another team's cluster", b, teamRef("o1", "t2", "ti-1"), "bnd-1", id},
		{"team principal without a team", b, identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ti-1", OrgID: "o1"}, "bnd-1", id},
		{"service principal with a team id", b, identity.PrincipalRef{Kind: identity.PrincipalService, ID: "s1", OrgID: "o1", TeamID: "t1"}, "bnd-1", id},
		{"unknown kind", b, identity.PrincipalRef{Kind: "robot", ID: "r1", OrgID: "o1"}, "bnd-1", id},
		{"no broker", nil, team, "bnd-1", id},
		{"static sign-in is the shared account", staticBroker, team, "bnd-1", staticID},
		{"invalid cluster id", b, team, "bnd-1", "bad/cluster"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanForPrincipal(tc.b, tc.principal, tc.binding, tc.cluster); err == nil {
				t.Fatal("the plan must be refused")
			}
		})
	}
	// The refusals that are about the plan itself are ErrPrincipalPlan (the rest are invalid input from other layers).
	for _, name := range []string{"user principal", "missing binding", "another team's cluster", "no broker", "static sign-in is the shared account"} {
		for _, tc := range tests {
			if tc.name != name {
				continue
			}
			if _, err := PlanForPrincipal(tc.b, tc.principal, tc.binding, tc.cluster); !errors.Is(err, ErrPrincipalPlan) {
				t.Errorf("%s: %v, want ErrPrincipalPlan", name, err)
			}
		}
	}
}

// A Service principal belongs to the organization: no team match is required to plan (the binding and grants decide).
func TestPlanForPrincipalServiceNeedsNoTeamMatch(t *testing.T) {
	id := technicalCluster(t)
	b := &principalBroker{scriptBroker: scriptBroker{kind: credential.KindOIDC}}
	if _, err := PlanForPrincipal(b, serviceRef("o1", "svc-1"), "bnd-9", id); err != nil {
		t.Fatal(err)
	}
}

// No fake AuthSession: the technical plan carries the broker and nothing of a user session.
func TestPlanForPrincipalHasNoAuthSession(t *testing.T) {
	id := technicalCluster(t)
	b := &principalBroker{scriptBroker: scriptBroker{kind: credential.KindOIDC}}
	plan, err := PlanForPrincipal(b, teamRef("o1", "t1", "ti-1"), "bnd-1", id)
	if err != nil {
		t.Fatal(err)
	}
	if plan.session.Broker != nil || plan.session.UserID != "" || plan.session.SessionID != "" || plan.session.OnAuthRequired != nil {
		t.Fatalf("a technical plan must not carry an AuthSession: %+v", plan.session)
	}
	if plan.principal == nil || plan.tokenBroker() != credential.Broker(b) {
		t.Fatal("the technical plan must carry the broker itself")
	}
}

// The pool key separates principal kind, principal id (with org and team), cluster and binding, and cannot clash with
// the legacy user key or the legacy team (bare cluster id) key.
func TestPrincipalPoolKeySeparation(t *testing.T) {
	keys := map[string]string{}
	add := func(name, key string) {
		t.Helper()
		if prev, dup := keys[key]; dup {
			t.Fatalf("pool key collision between %q and %q: %q", prev, name, key)
		}
		keys[key] = name
		if strings.Contains(key, canary) {
			t.Fatalf("a secret in a pool key: %q", key)
		}
	}
	base := teamRef("o1", "t1", "x")
	add("team x / c1 / b1", principalPoolKey("c1", base, "b1"))
	add("service x / c1 / b1 (same textual id, other kind)", principalPoolKey("c1", serviceRef("o1", "x"), "b1"))
	add("team x / c2 / b1 (other cluster)", principalPoolKey("c2", base, "b1"))
	add("team x / c1 / b2 (other binding)", principalPoolKey("c1", base, "b2"))
	add("team y / c1 / b1 (other principal)", principalPoolKey("c1", teamRef("o1", "t1", "y"), "b1"))
	add("team x of other org", principalPoolKey("c1", teamRef("o2", "t1", "x"), "b1"))
	add("team x of other team", principalPoolKey("c1", teamRef("o1", "t2", "x"), "b1"))
	add("legacy team account c1", poolKey("c1", credential.Subject{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "t1"}))
	add("legacy user x on c1", poolKey("c1", credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "x"}))
	add("legacy user b1 on c1", poolKey("c1", credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "b1"}))

	// Field boundaries cannot be shifted: ids contain no NUL, but the order of the fields still must matter.
	a := principalPoolKey("c1", teamRef("o1", "t1", "ab"), "c")
	b := principalPoolKey("c1", teamRef("o1", "t1", "a"), "bc")
	if a == b {
		t.Fatal("moving a character between the principal id and the binding must change the key")
	}
}

// The shared pool never serves a technical connection to another principal, binding, or to a user.
func TestPoolIsolationForTechnicalConnections(t *testing.T) {
	id := technicalCluster(t)
	m, builds, _ := poolFixture(t)
	b := &principalBroker{scriptBroker: scriptBroker{kind: credential.KindOIDC}}

	get := func(plan ConnPlan) *ClusterConnection {
		t.Helper()
		c, err := m.pool.getOrCreate(plan.Key, func() (*ClusterConnection, error) { return m.build(plan) })
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	plan := func(p identity.PrincipalRef, binding string) ConnPlan {
		t.Helper()
		pl, err := PlanForPrincipal(b, p, binding, id)
		if err != nil {
			t.Fatal(err)
		}
		return pl
	}

	team := get(plan(teamRef("o1", "t1", "ti-1"), "bnd-1"))
	if again := get(plan(teamRef("o1", "t1", "ti-1"), "bnd-1")); again != team || builds.Load() != 1 {
		t.Fatal("the same technical subject shares its own connection")
	}
	svc := get(plan(serviceRef("o1", "ti-1"), "bnd-1")) // same textual id and binding id, other kind
	rebound := get(plan(teamRef("o1", "t1", "ti-1"), "bnd-2"))
	other := get(plan(teamRef("o1", "t1", "ti-2"), "bnd-1"))

	// A user and the legacy team account on the same cluster.
	view := viewFor(m, &scriptBroker{kind: credential.KindOIDC}, "alice")
	_, user, err := view.connect(id)
	if err != nil {
		t.Fatal(err)
	}

	conns := map[*ClusterConnection]string{}
	for name, c := range map[string]*ClusterConnection{"team": team, "service": svc, "rebound": rebound, "other": other, "user": user} {
		if prev, dup := conns[c]; dup {
			t.Fatalf("%s and %s share one connection", prev, name)
		}
		conns[c] = name
	}
	if builds.Load() != 5 {
		t.Fatalf("every subject builds its own connection: %d builds", builds.Load())
	}

	// Dropping one technical connection leaves the others.
	m.DropPrincipalConnection(id, teamRef("o1", "t1", "ti-1"), "bnd-1")
	if m.pool.has(team.key) {
		t.Fatal("the dropped technical connection must leave the pool")
	}
	for name, c := range map[string]*ClusterConnection{"service": svc, "rebound": rebound, "other": other, "user": user} {
		if !m.pool.has(c.key) {
			t.Fatalf("%s must stay in the pool", name)
		}
	}
}

// The transport of a technical plan asks the broker with the technical subject, never calls an interactive callback, and a
// missing credential blocks the request: no user credential, no legacy team credential.
func TestPrincipalTransportUsesTheTechnicalSubjectAndBlocksWithoutFallback(t *testing.T) {
	id := technicalCluster(t)
	b := &principalBroker{scriptBroker: scriptBroker{kind: credential.KindOIDC}}
	plan, err := PlanForPrincipal(b, serviceRef("o1", "svc-1"), "bnd-1", id)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ConfigFor(plan)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExecProvider != nil || cfg.AuthProvider != nil || cfg.BearerToken != "" || cfg.WrapTransport == nil {
		t.Fatal("exec, auth-provider and tokens stay out of the configuration; the token comes from the transport")
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg), canary) {
		t.Fatal("no secret in the configuration")
	}

	var sent []string
	rt := brokerTransport(plan)(rtFunc(func(r *http.Request) (*http.Response, error) {
		sent = append(sent, r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if len(b.subjects) != 1 {
		t.Fatalf("the broker was asked %d times", len(b.subjects))
	}
	if got := b.subjects[0]; got != plan.Subject || got.Scope != credential.ScopePrincipal || got.OwnerID != "" || got.SessionID != "" {
		t.Fatalf("the broker must receive exactly the technical subject, got %+v", got)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "Bearer ") {
		t.Fatalf("headers %v", sent)
	}

	// The broker refuses (nothing stored, binding revoked, outcome unconfirmed, ...): the request is not sent.
	b.err = credential.ErrChallengeRequired
	blocked := brokerTransport(plan)(rtFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("a request without a technical token must not be sent (no fallback credential)")
		return nil, nil
	}))
	if _, err := blocked.RoundTrip(req); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("got %v", err)
	}
	b.err = credential.ErrBindingUnusable
	if _, err := blocked.RoundTrip(req); !errors.Is(err, credential.ErrBindingUnusable) {
		t.Fatalf("got %v", err)
	}
}

// A 401 invalidates the technical token with the full technical key (not a legacy-shaped one) and does not ask a human.
func TestPrincipalTransportInvalidatesWithTheTechnicalKey(t *testing.T) {
	id := technicalCluster(t)
	inv := &principalInvalidatingBroker{principalBroker: principalBroker{scriptBroker: scriptBroker{kind: credential.KindOIDC}}}
	plan, err := PlanForPrincipal(inv, teamRef("o1", "t1", "ti-1"), "bnd-1", id)
	if err != nil {
		t.Fatal(err)
	}
	rt := brokerTransport(plan)(rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	want := credential.Key{ClusterID: id, Scope: credential.ScopePrincipal, Principal: teamRef("o1", "t1", "ti-1"), BindingID: "bnd-1"}
	if len(inv.keys) != 1 || inv.keys[0] != want {
		t.Fatalf("invalidated keys: %+v, want %+v", inv.keys, want)
	}
}

type principalInvalidatingBroker struct {
	principalBroker
	keys []credential.Key
}

func (b *principalInvalidatingBroker) Invalidate(k credential.Key) { b.keys = append(b.keys, k) }
