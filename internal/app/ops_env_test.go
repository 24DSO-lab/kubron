// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
)

// beginOp: чужой кластер неотличим от несуществующего, а отказ по праву пишется в аудит; до проверки подключение
// к кластеру не строится (иначе чужая команда могла бы вызвать обращение к кластеру).
func TestBeginOpChecksTeamAndPermissionBeforeConnecting(t *testing.T) {
	k8s.SetKubronDir(t.TempDir())
	t.Cleanup(func() { k8s.SetKubronDir("") })
	a := newAuditedApp(t)
	a.auth.SetOnLogout(nil)

	if _, err := a.auth.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	u, err := a.auth.CreateUser("ops", testPass, auth.RoleUser)
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
		roles:    map[string]map[string]auth.Role{u.ID: {"red": auth.RoleUser}},
	})
	a.auth.Logout()
	if _, err := a.auth.Login(t.Context(), "ops", testPass); err != nil {
		t.Fatal(err)
	}
	if err := a.auth.ChangePassword(t.Context(), testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}

	// Чужая команда и несуществующий кластер.
	for _, id := range []string{blue.ID, "ghost"} {
		_, op, err := a.beginOp(opsplatform.OpRequest{Module: "incident", Operation: "open", ClusterID: id, Permission: auth.PermRead})
		if !errors.Is(err, auth.ErrClusterNotFound) || op != nil {
			t.Fatalf("%s: op=%v err=%v, want ErrClusterNotFound", id, op, err)
		}
	}
	// Член команды без права diagnostics.logs.
	_, op, err := a.beginOp(opsplatform.OpRequest{Module: "diagnose", Operation: "logs", ClusterID: red.ID, Permission: auth.PermDiagnosticsLogs})
	if !errors.Is(err, auth.ErrForbidden) || op != nil {
		t.Fatalf("op=%v err=%v, want ErrForbidden", op, err)
	}
	last := events(t, a)[0]
	if last.Result != audit.ResultDenied || last.Action != "DiagnoseLogs" || last.TeamID != "red" || last.ClusterID != red.ID {
		t.Fatalf("denial record: %+v", last)
	}
}

func TestOpsEnvScopeInDesktopMode(t *testing.T) {
	a := newAuditedApp(t)
	env := opsEnv{a}
	if env.OrgID() != localOrgID || env.DefaultTeam() != localDefaultTeam {
		t.Fatalf("desktop scope = %q/%q", env.OrgID(), env.DefaultTeam())
	}
}

// Событие уходит только через уведомитель вида-владельца; у вида без уведомителя оно не доставляется никому.
func TestOpsEnvNotifyIsAddressedToTheView(t *testing.T) {
	root := newAuditedApp(t)
	viewA, viewB := *root, *root
	viewA.k8sMgr, viewB.k8sMgr = root.k8sMgr.ForSession(), root.k8sMgr.ForSession()
	var gotA, gotB []string
	viewA.k8sMgr.SetNotifier(func(event string, data ...any) { gotA = append(gotA, event) })
	viewB.k8sMgr.SetNotifier(func(event string, data ...any) { gotB = append(gotB, event) })

	opsEnv{&viewA}.Notify("incident:updated", "inc-1")
	if len(gotA) != 1 || len(gotB) != 0 {
		t.Fatalf("A=%v B=%v: the event must reach only the owner view", gotA, gotB)
	}
	opsEnv{root}.Notify("incident:updated", "inc-2") // у корневого вида нет уведомителя: никому
	if len(gotA) != 1 || len(gotB) != 0 {
		t.Fatalf("a view without a notifier must not broadcast: A=%v B=%v", gotA, gotB)
	}
}
