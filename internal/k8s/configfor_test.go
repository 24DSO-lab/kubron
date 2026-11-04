// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const canary = "CANARY-7f3a9c-do-not-leak"

// scriptBroker is a credential.Broker whose Describe and Token are scripted.
type scriptBroker struct {
	kind   credential.Kind
	tokens atomic.Int32
	err    error
}

func (b *scriptBroker) Describe([]byte, string) (credential.Descriptor, error) {
	return credential.Descriptor{Kind: b.kind}, nil
}
func (b *scriptBroker) Acquire(context.Context, credential.Subject, credential.Descriptor) (credential.Result, error) {
	return credential.Result{}, nil
}
func (b *scriptBroker) Answer(context.Context, credential.Subject, string, credential.Answer) (credential.Result, error) {
	return credential.Result{}, nil
}
func (b *scriptBroker) Poll(context.Context, credential.Subject, string) (credential.Result, error) {
	return credential.Result{}, nil
}
func (b *scriptBroker) Cancel(context.Context, credential.Subject, string) error { return nil }
func (b *scriptBroker) Token(_ context.Context, s credential.Subject, _ credential.Descriptor) (credential.Token, error) {
	b.tokens.Add(1)
	if b.err != nil {
		return credential.Token{}, b.err
	}
	return credential.Token{Value: canary + "-" + s.OwnerID}, nil
}
func (b *scriptBroker) Revoke(context.Context, credential.RevokeFilter) error { return nil }
func (b *scriptBroker) List(context.Context, string) ([]credential.ConnectionInfo, error) {
	return nil, nil
}

// execKubeconfig signs in with an exec plugin: a broker-managed kind whose block must never reach rest.Config.
const execKubeconfig = `apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: https://203.0.113.10:6443
    insecure-skip-tls-verify: true
contexts:
- name: ctx
  context:
    cluster: c
    user: u
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: some-plugin
      args: ["get-token"]
      env:
      - name: SECRET_ENV
        value: ` + canary + `
`

func storedCluster(t *testing.T) string {
	t.Helper()
	SetKubronDir(t.TempDir())
	t.Cleanup(func() { SetKubronDir("") })
	meta, err := AddClusterFromText(sampleKubeconfig, ClusterSettings{DisplayName: "c", ContextName: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

// poolFixture: a root manager whose builds are counted and whose connections remember their plan key.
func poolFixture(t *testing.T) (*Manager, *atomic.Int32, *sync.Map) {
	t.Helper()
	m := NewManager()
	builds := &atomic.Int32{}
	closed := &sync.Map{}
	m.build = func(p ConnPlan) (*ClusterConnection, error) {
		builds.Add(1)
		return &ClusterConnection{Name: p.ClusterID, key: p.Key}, nil
	}
	m.pool.closeFn = func(c *ClusterConnection) { closed.Store(c.key, true) }
	return m, builds, closed
}

func viewFor(m *Manager, b credential.Broker, user string) *Manager {
	v := m.ForSession()
	v.SetAuthSession(AuthSession{Broker: b, UserID: user})
	return v
}

// Two users with different rights must never share a connection (and so an informer).
func TestPersonalConnectionsAreNotSharedBetweenUsers(t *testing.T) {
	id := storedCluster(t)
	m, builds, _ := poolFixture(t)
	b := &scriptBroker{kind: credential.KindOIDC}
	alice, bob, alice2 := viewFor(m, b, "alice"), viewFor(m, b, "bob"), viewFor(m, b, "alice")
	for _, v := range []*Manager{alice, bob, alice2} {
		if err := v.ConnectToCluster(id); err != nil {
			t.Fatal(err)
		}
	}
	ca, _ := alice.GetActiveConnection()
	cb, _ := bob.GetActiveConnection()
	ca2, _ := alice2.GetActiveConnection()
	if ca == nil || cb == nil || ca == cb {
		t.Fatal("alice and bob must get different connections")
	}
	if ca != ca2 {
		t.Fatal("two views of one user share the connection")
	}
	if builds.Load() != 2 {
		t.Fatalf("expected 2 builds (one per user), got %d", builds.Load())
	}
	// Status "connected" is personal as well.
	carol := viewFor(m, b, "carol")
	if _, ok := carol.lookupConn(id); ok {
		t.Fatal("carol has no connection of her own and must not see alice's")
	}
}

// Team-owned (static) credentials keep one shared connection per cluster.
func TestTeamConnectionIsShared(t *testing.T) {
	id := storedCluster(t)
	m, builds, _ := poolFixture(t)
	b := &scriptBroker{kind: credential.KindStatic}
	for _, u := range []string{"alice", "bob", "carol"} {
		if err := viewFor(m, b, u).ConnectToCluster(id); err != nil {
			t.Fatal(err)
		}
	}
	if builds.Load() != 1 {
		t.Fatalf("a team connection is built once, got %d", builds.Load())
	}
}

// Load: many subjects on one cluster. Each gets its own entry, and when all leave, the idle TTL closes every one.
func TestManySubjectsOnOneClusterAreClosedByIdleTTL(t *testing.T) {
	id := storedCluster(t)
	m, builds, closed := poolFixture(t)
	m.pool.idleTTL = 30 * time.Millisecond
	b := &scriptBroker{kind: credential.KindOIDC}
	const n = 50
	views := make([]*Manager, n)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range views {
		views[i] = viewFor(m, b, fmt.Sprintf("user-%d", i))
		wg.Add(1)
		go func(v *Manager) {
			defer wg.Done()
			if err := v.ConnectToCluster(id); err != nil {
				errs <- err
			}
		}(views[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if builds.Load() != n {
		t.Fatalf("expected %d builds, got %d", n, builds.Load())
	}
	m.pool.mu.Lock()
	entries := len(m.pool.entries)
	m.pool.mu.Unlock()
	if entries != n {
		t.Fatalf("pool entries %d, want %d", entries, n)
	}
	for _, v := range views {
		v.Detach()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		count := 0
		closed.Range(func(_, _ any) bool { count++; return true })
		if count == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d idle connections were closed", count, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Removing a cluster closes the connections of all subjects.
func TestRemoveConnectionDropsEverySubject(t *testing.T) {
	id := storedCluster(t)
	m, _, closed := poolFixture(t)
	b := &scriptBroker{kind: credential.KindOIDC}
	a, c := viewFor(m, b, "alice"), viewFor(m, b, "bob")
	for _, v := range []*Manager{a, c} {
		if err := v.ConnectToCluster(id); err != nil {
			t.Fatal(err)
		}
	}
	if !a.RemoveConnection(id) {
		t.Fatal("the cluster was active")
	}
	n := 0
	closed.Range(func(_, _ any) bool { n++; return true })
	if n != 2 {
		t.Fatalf("closed %d connections, want 2", n)
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The token comes from the broker on every request, is not kept in the configuration and is not in errors.
func TestBrokerTransportAsksTheBrokerOnEveryRequest(t *testing.T) {
	b := &scriptBroker{kind: credential.KindOIDC}
	p := ConnPlan{ClusterID: "c", Subject: credential.Subject{ClusterID: "c", Scope: credential.ScopeUser, OwnerID: "alice"},
		Desc: credential.Descriptor{Kind: credential.KindOIDC}, session: AuthSession{Broker: b}}
	var got []string
	rt := brokerTransport(p)(rtFunc(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	for i := 0; i < 3; i++ {
		if _, err := rt.RoundTrip(req); err != nil {
			t.Fatal(err)
		}
	}
	if b.tokens.Load() != 3 || len(got) != 3 || got[0] != "Bearer "+canary+"-alice" {
		t.Fatalf("tokens=%d headers=%v", b.tokens.Load(), got)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("the caller's request must not be modified")
	}
}

func TestBrokerTransportReportsMissingSignIn(t *testing.T) {
	b := &scriptBroker{kind: credential.KindOIDC, err: credential.ErrChallengeRequired}
	var asked []string
	p := ConnPlan{ClusterID: "c", session: AuthSession{Broker: b, OnAuthRequired: func(id string) { asked = append(asked, id) }}}
	rt := brokerTransport(p)(rtFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("a request without a token must not be sent")
		return nil, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	if _, err := rt.RoundTrip(req); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("got %v", err)
	}
	if len(asked) != 1 || asked[0] != "c" {
		t.Fatalf("OnAuthRequired: %v", asked)
	}
}

// ConfigFor never lets an exec block or a token into the configuration of a broker-managed sign-in.
func TestConfigForStripsExecAndHoldsNoToken(t *testing.T) {
	SetKubronDir(t.TempDir())
	t.Cleanup(func() { SetKubronDir("") })
	meta, err := AddClusterFromText(execKubeconfig, ClusterSettings{DisplayName: "m", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanFor(AuthSession{Broker: &scriptBroker{kind: credential.KindOIDC}, UserID: "alice"}, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ConfigFor(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExecProvider != nil || cfg.AuthProvider != nil {
		t.Fatal("exec and auth-provider must be removed from the configuration")
	}
	if cfg.BearerToken != "" || strings.Contains(fmt.Sprintf("%+v", cfg), canary) || cfg.WrapTransport == nil {
		t.Fatal("the token must come from the transport, not the configuration")
	}
	if p.Subject.Scope != credential.ScopeUser || p.Key == meta.ID {
		t.Fatalf("a personal sign-in needs a personal pool key: %+v", p)
	}
}

// TestRestConfigIsBuiltOnlyInConfigFor: every consumer gets its configuration through ConfigFor. Building a
// rest.Config anywhere else would bypass the broker (and with it per-user credentials).
func TestRestConfigIsBuiltOnlyInConfigFor(t *testing.T) {
	// Files allowed to build one, with the reason.
	allowed := map[string]string{
		"internal/k8s/configfor.go":        "ConfigFor itself",
		"internal/k8s/connection.go":       "in-cluster and oidc-password mode configs, called only from legacyRestConfig",
		"internal/k8s/cluster_settings.go": "test of a kubeconfig that is not stored yet (not a session consumer)",
		"cmd/oidc-spike/main.go":           "standalone spike tool",
	}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var bad []string
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if _, ok := allowed[rel]; ok {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Errorf("parsing %s: %v", rel, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Config" && identName(sel.X) == "rest" {
						bad = append(bad, rel+": rest.Config literal")
					}
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
						switch {
						case sel.Sel.Name == "ClientConfig" && len(x.Args) == 0:
							bad = append(bad, rel+": ClientConfig()")
						case sel.Sel.Name == "InClusterConfig" && identName(sel.X) == "rest":
							bad = append(bad, rel+": rest.InClusterConfig()")
						case sel.Sel.Name == "BuildConfigFromFlags" && identName(sel.X) == "clientcmd":
							bad = append(bad, rel+": clientcmd.BuildConfigFromFlags")
						}
					}
				}
				return true
			})
			return nil
		})
	}
	if len(bad) > 0 {
		t.Errorf("a rest.Config is built outside ConfigFor (use k8s.ConfigFor / Manager.ConnectToCluster): %v", bad)
	}
}

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// Whose credentials a cluster has: static and the shared account of oidc-password belong to the team, everything
// else to the user.
func TestScopeFor(t *testing.T) {
	for _, c := range []struct {
		name  string
		d     credential.Descriptor
		src   ClusterAuthSource
		scope credential.Scope
		owner string
	}{
		{"static", credential.Descriptor{Kind: credential.KindStatic}, ClusterAuthSource{TeamID: "t1"}, credential.ScopeTeam, "t1"},
		{"static without teams", credential.Descriptor{Kind: credential.KindStatic}, ClusterAuthSource{}, credential.ScopeTeam, "default"},
		{"oidc", credential.Descriptor{Kind: credential.KindOIDC}, ClusterAuthSource{TeamID: "t1"}, credential.ScopeUser, "u1"},
		{"oidc-password mode is the team's account", credential.Descriptor{Kind: credential.KindOIDC},
			ClusterAuthSource{Mode: ModeOIDCPassword, TeamID: "t1"}, credential.ScopeTeam, "t1"},
		{"cloud", credential.Descriptor{Kind: credential.KindEKS}, ClusterAuthSource{TeamID: "t1"}, credential.ScopeUser, "u1"},
	} {
		if sc, owner := scopeFor(c.d, c.src, "u1"); sc != c.scope || owner != c.owner {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, sc, owner, c.scope, c.owner)
		}
	}
}

// invalidatingBroker is a scriptBroker that also implements credential.Invalidator.
type invalidatingBroker struct {
	*scriptBroker
	invalidated []credential.Key
	afterErr    error // Token error once the token was invalidated
}

func (b *invalidatingBroker) Invalidate(k credential.Key) {
	b.invalidated = append(b.invalidated, k)
	if b.afterErr != nil {
		b.scriptBroker.err = b.afterErr
	}
}

func unauthorizedPlan(b credential.Broker, asked *[]string) ConnPlan {
	return ConnPlan{ClusterID: "c", Subject: credential.Subject{ClusterID: "c", Scope: credential.ScopeUser, OwnerID: "alice"},
		Desc:    credential.Descriptor{Kind: credential.KindExec},
		session: AuthSession{Broker: b, OnAuthRequired: func(id string) { *asked = append(*asked, id) }}}
}

func TestBrokerTransportInvalidatesTheTokenOn401WithoutRetry(t *testing.T) {
	b := &invalidatingBroker{scriptBroker: &scriptBroker{kind: credential.KindExec}}
	var asked []string
	sent := 0
	rt := brokerTransport(unauthorizedPlan(b, &asked))(rtFunc(func(*http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the 401 must reach the caller: %v, %v", resp, err)
	}
	if sent != 1 {
		t.Fatalf("the request was sent %d times, want 1 (no retry)", sent)
	}
	want := credential.Key{ClusterID: "c", Scope: credential.ScopeUser, OwnerID: "alice"}
	if len(b.invalidated) != 1 || b.invalidated[0] != want {
		t.Fatalf("invalidated = %v", b.invalidated)
	}
	if len(asked) != 0 {
		t.Fatalf("a new token was available, but the interface was asked to sign in: %v", asked)
	}
}

func TestBrokerTransportAsksForSignInWhenNothingIsLeftAfter401(t *testing.T) {
	b := &invalidatingBroker{scriptBroker: &scriptBroker{kind: credential.KindExec}, afterErr: credential.ErrChallengeRequired}
	var asked []string
	rt := brokerTransport(unauthorizedPlan(b, &asked))(rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "c" {
		t.Fatalf("OnAuthRequired = %v", asked)
	}
}

func TestBrokerTransportLeavesOtherStatusesAlone(t *testing.T) {
	b := &invalidatingBroker{scriptBroker: &scriptBroker{kind: credential.KindExec}}
	var asked []string
	rt := brokerTransport(unauthorizedPlan(b, &asked))(rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	if _, err := rt.RoundTrip(req); err != nil || len(b.invalidated) != 0 {
		t.Fatalf("403 must not invalidate: %v, %v", err, b.invalidated)
	}
}

// A broker without Invalidate (the older contract) keeps working: a 401 is passed through.
func TestBrokerTransport401WithoutInvalidatorIsPassedThrough(t *testing.T) {
	b := &scriptBroker{kind: credential.KindExec}
	var asked []string
	rt := brokerTransport(unauthorizedPlan(b, &asked))(rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest("GET", "https://203.0.113.10/api", nil)
	if resp, err := rt.RoundTrip(req); err != nil || resp.StatusCode != 401 || b.tokens.Load() != 1 {
		t.Fatalf("%v, %v, tokens=%d", resp, err, b.tokens.Load())
	}
}
