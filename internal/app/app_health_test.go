// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"sync"
	"testing"

	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// toggleAccess — доступ к одному кластеру, который можно отобрать на ходу.
type toggleAccess struct {
	mu      sync.Mutex
	cluster string
	member  bool
}

func (t *toggleAccess) set(member bool) {
	t.mu.Lock()
	t.member = member
	t.mu.Unlock()
}

func (t *toggleAccess) ClusterTeam(id string) (string, bool) { return "team-1", id == t.cluster }
func (t *toggleAccess) TeamRole(_, _ string) (auth.Role, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return auth.RoleAdmin, t.member
}
func (t *toggleAccess) TeamRoles(string) map[string]auth.Role {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.member {
		return nil
	}
	return map[string]auth.Role{"team-1": auth.RoleAdmin}
}
func (t *toggleAccess) IsOrgAdmin(string) bool { return false }

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:1"}
users:
- name: u
  user: {token: abc}
contexts:
- name: ctx
  context: {cluster: c, user: u}
current-context: ctx
`

func TestEnforceAccessClosesResourcesOfAUserWhoLostTheCluster(t *testing.T) {
	k8s.SetKubronDir(t.TempDir())
	t.Cleanup(func() { k8s.SetKubronDir("") })

	a := newAuditedApp(t)
	a.pfMgr = k8s.NewPortForwardManager(a.k8sMgr)
	if _, err := a.auth.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	meta, err := k8s.AddClusterFromText(testKubeconfig, k8s.ClusterSettings{DisplayName: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	acc := &toggleAccess{cluster: meta.ID, member: true}
	a.auth.SetAccess(acc)

	var mu sync.Mutex
	var got []string
	a.k8sMgr.SetNotifier(func(event string, data ...any) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, event)
		if len(data) == 1 {
			if id, ok := data[0].(string); ok {
				got = append(got, id)
			}
		}
	})

	if err := a.k8sMgr.ConnectToCluster(meta.ID); err != nil {
		t.Fatal(err)
	}
	if EnforceAccess(a) {
		t.Fatal("a member must keep the cluster")
	}
	if a.k8sMgr.GetActiveContext() != meta.ID {
		t.Fatal("the active cluster must stay")
	}

	acc.set(false) // исключили из команды или кластер перенесли
	if !EnforceAccess(a) {
		t.Fatal("access was lost, resources must be closed")
	}
	if a.k8sMgr.GetActiveContext() != "" {
		t.Fatalf("the active cluster must be reset, got %q", a.k8sMgr.GetActiveContext())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != EventClusterAccessRevoked || got[1] != meta.ID {
		t.Fatalf("the client must be told which cluster was closed: %v", got)
	}
	if EnforceAccess(a) {
		t.Fatal("nothing left to close on the next sweep")
	}
}

func TestEnforceAccessIgnoresViewsWithoutSession(t *testing.T) {
	if EnforceAccess(nil) {
		t.Fatal("nil view")
	}
	if EnforceAccess(&App{}) {
		t.Fatal("a view without authentication has nothing to close")
	}
}
