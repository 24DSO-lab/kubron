// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// auditAccess — членство в командах для теста: кластер → команда, пользователь → роли по командам.
type auditAccess struct {
	clusters map[string]string
	roles    map[string]map[string]auth.Role
}

func (f auditAccess) ClusterTeam(id string) (string, bool) { t, ok := f.clusters[id]; return t, ok }
func (f auditAccess) TeamRole(u, team string) (auth.Role, bool) {
	r, ok := f.roles[u][team]
	return r, ok
}
func (f auditAccess) TeamRoles(u string) map[string]auth.Role { return f.roles[u] }
func (f auditAccess) IsOrgAdmin(string) bool                  { return false }

const kubeconfigForTest = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://203.0.113.10:6443
    insecure-skip-tls-verify: true
users:
- name: u
  user:
    token: t
contexts:
- name: ctx
  context:
    cluster: c
    user: u
current-context: ctx
`

// Состояние Helm принадлежит команде активного кластера, поэтому и право им управлять проверяется по ней. «Админ
// хотя бы одной команды» не годится: админ Blue с ролью «пользователь» в Red не должен менять репозитории Red.
// Заодно проверяем, что события аудита несут команду и кластер.
func TestHelmManagementIsCheckedAgainstTheTeamOfTheActiveCluster(t *testing.T) {
	k8s.SetKubronDir(t.TempDir())
	t.Cleanup(func() { k8s.SetKubronDir("") })
	a := newAuditedApp(t)
	a.auth.SetOnLogout(nil)

	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  app:\n    - name: app\n      version: 1.0.0\n      urls: [app-1.0.0.tgz]\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer repo.Close()

	if _, err := a.auth.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	ops, err := a.auth.CreateUser("ops", testPass, auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	red, err := k8s.AddClusterFromText(kubeconfigForTest, k8s.ClusterSettings{DisplayName: "red", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	blue, err := k8s.AddClusterFromText(kubeconfigForTest, k8s.ClusterSettings{DisplayName: "blue", ContextName: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	a.auth.SetAccess(auditAccess{
		clusters: map[string]string{red.ID: "red", blue.ID: "blue"},
		roles:    map[string]map[string]auth.Role{ops.ID: {"blue": auth.RoleAdmin, "red": auth.RoleUser}},
	})

	a.auth.Logout()
	if _, err := a.auth.Login("ops", testPass); err != nil {
		t.Fatal(err)
	}
	if err := a.auth.ChangePassword(testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}

	// Активен кластер команды Red, где у ops только чтение: любое управление репозиториями запрещено.
	if err := a.k8sMgr.ConnectToCluster(red.ID); err != nil {
		t.Fatal(err)
	}
	denied := map[string]func() error{
		"AddHelmRepo":        func() error { return a.AddHelmRepo("x", repo.URL, "", "", false) },
		"RemoveHelmRepo":     func() error { return a.RemoveHelmRepo("x") },
		"UpdateHelmRepos":    func() error { _, err := a.UpdateHelmRepos(""); return err },
		"AddHelmOCIRepo":     func() error { return a.AddHelmOCIRepo("x", "oci://reg.example/c", []string{"a"}, "", "") },
		"AddHelmOCIChart":    func() error { return a.AddHelmOCIChart("x", "a") },
		"RemoveHelmOCIChart": func() error { return a.RemoveHelmOCIChart("x", "a") },
		"ClearHelmOperations": func() error {
			return a.ClearHelmOperations()
		},
	}
	for name, call := range denied {
		if err := call(); !errors.Is(err, auth.ErrForbidden) {
			t.Errorf("%s with the Red cluster active: %v, want forbidden", name, err)
		}
	}
	if l, err := a.ListHelmRepos(); err != nil || len(l) != 0 {
		t.Errorf("reading is allowed for a member of the team: %v %v", l, err)
	}
	last := events(t, a)[0]
	if last.Result != audit.ResultDenied || last.TeamID != "red" || last.ClusterID != red.ID {
		t.Errorf("a denial must name the team and the cluster: %+v", last)
	}

	// Активен кластер команды Blue, где ops — администратор: можно, и события несут команду и кластер.
	if err := a.k8sMgr.ConnectToCluster(blue.ID); err != nil {
		t.Fatal(err)
	}
	before := len(events(t, a))
	if err := a.AddHelmRepo("x", repo.URL, "", "", false); err != nil {
		t.Fatalf("admin of the active cluster's team: %v", err)
	}
	if err := a.RemoveHelmRepo("x"); err != nil {
		t.Fatal(err)
	}
	ev := events(t, a)
	if len(ev) != before+2 {
		t.Fatalf("expected two new audit records, got %d", len(ev)-before)
	}
	for i, action := range []string{"RemoveHelmRepo", "AddHelmRepo"} {
		e := ev[i]
		if e.Action != action || e.Result != audit.ResultOK || e.TeamID != "blue" || e.ClusterID != blue.ID || e.Detail != "name=x" {
			t.Errorf("record %d: %+v", i, e)
		}
	}
	// В детали попадает только имя репозитория: ни URL, ни пароль.
	if err := a.AddHelmRepo("y", repo.URL, "ci", "never-in-the-journal", false); err != nil {
		t.Fatal(err)
	}
	if d := events(t, a)[0].Detail; d != "name=y" {
		t.Errorf("the audit detail must not carry credentials: %q", d)
	}
}
