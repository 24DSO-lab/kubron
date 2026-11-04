// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
	"github.com/24DSO-labs/kubron/internal/credential"
)

func signinKubeconfig(server string, insecure bool) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: %s
    insecure-skip-tls-verify: %t
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
`, server, insecure)
}

func clusterAt(t *testing.T, server string, insecure bool) string {
	t.Helper()
	SetKubronDir(t.TempDir())
	t.Cleanup(func() { SetKubronDir("") })
	meta, err := AddClusterFromText(signinKubeconfig(server, insecure), ClusterSettings{DisplayName: "c", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

func statusOf(t *testing.T, m *Manager, id string) ClusterStatus {
	t.Helper()
	for _, s := range m.GetClusterStatuses() {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("cluster %s is not listed", id)
	return ClusterStatus{}
}

// The background check of a personally-signed cluster checks the address and TLS only: no token is requested and
// none is sent, and an answer 401 means "reachable".
func TestPersonalClusterProbeChecksAddressAndTLSOnly(t *testing.T) {
	var sawAuth atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth.Add(1)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	id := clusterAt(t, ts.URL, true)
	b := &scriptBroker{kind: credential.KindOIDC}
	m := NewManager()
	m.SetAuthSession(AuthSession{Broker: b})

	h := m.probeCluster(context.Background(), id)
	if h.State != clusterhealth.OK {
		t.Fatalf("a reachable cluster that answers 401 is reachable: %+v", h)
	}
	if b.tokens.Load() != 0 || sawAuth.Load() != 0 {
		t.Fatalf("the background check must not use credentials: tokens=%d sent=%d", b.tokens.Load(), sawAuth.Load())
	}
}

func TestPersonalClusterProbeReportsTLSAndConnectionFailures(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	id := clusterAt(t, ts.URL, false) // self-signed certificate that the kubeconfig does not trust
	m := NewManager()
	m.SetAuthSession(AuthSession{Broker: &scriptBroker{kind: credential.KindOIDC}})
	if h := m.probeCluster(context.Background(), id); h.State != clusterhealth.Unreachable {
		t.Fatalf("an untrusted certificate must be reported: %+v", h)
	}
	ts.Close()
	id = clusterAt(t, ts.URL, true)
	if h := m.probeCluster(context.Background(), id); h.State != clusterhealth.Unreachable {
		t.Fatalf("a closed server is unreachable: %+v", h)
	}
}

// "Sign-in required" and "unreachable" are different states, and the first is personal.
func TestClusterStatusDistinguishesSignInFromUnreachable(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer ts.Close()
	id := clusterAt(t, ts.URL, true)
	b := &scriptBroker{kind: credential.KindOIDC}
	m := NewManager()
	m.SetAuthSession(AuthSession{Broker: b})
	var plan ConnPlan
	m.build = func(p ConnPlan) (*ClusterConnection, error) {
		plan = p
		return &ClusterConnection{Name: p.ClusterID, key: p.Key}, nil
	}
	h := m.probeCluster(context.Background(), id)
	m.Health().Set(id, h)

	alice := viewFor(m, b, "alice")
	st := statusOf(t, alice, id)
	if !st.AuthRequired || st.Connected {
		t.Fatalf("alice has not signed in: %+v", st)
	}
	if st.Health == nil || st.Health.State != clusterhealth.OK || st.Error != "" {
		t.Fatalf("the cluster itself is reachable: %+v", st.Health)
	}

	if err := alice.ConnectToCluster(id); err != nil {
		t.Fatal(err)
	}
	if st = statusOf(t, alice, id); st.AuthRequired || !st.Connected {
		t.Fatalf("after signing in: %+v", st)
	}
	if st = statusOf(t, viewFor(m, b, "bob"), id); !st.AuthRequired || st.Connected {
		t.Fatalf("alice's sign-in is not bob's: %+v", st)
	}

	plan.session.OnAuthRequired(id) // the broker reports that alice's saved sign-in is gone
	if st = statusOf(t, alice, id); !st.AuthRequired {
		t.Fatalf("a lost sign-in must be reported: %+v", st)
	}
}
