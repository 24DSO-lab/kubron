// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func memStoreWithHome(t *testing.T) {
	t.Helper()
	setupClustersHome(t)
	useClusterStore(t, newMemClusterStore())
}

func TestAddClusterWithSharedAccount(t *testing.T) {
	memStoreWithHome(t)

	meta, err := AddClusterFromTextToTeam(execKubeconfig, ClusterSettings{DisplayName: "shared", SharedAccount: true}, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Mode != ModeTeamAccount || meta.TeamID != "t1" {
		t.Fatalf("got mode %q team %q", meta.Mode, meta.TeamID)
	}
	p, err := PlanFor(AuthSession{Broker: &scriptBroker{kind: credential.KindOIDC}, UserID: "alice"}, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject.Scope != credential.ScopeTeam || p.Subject.OwnerID != "t1" || p.Key != meta.ID {
		t.Errorf("the shared account belongs to the team: %+v", p.Subject)
	}

	// Without the flag the sign-in stays personal.
	meta, err = AddClusterFromTextToTeam(execKubeconfig, ClusterSettings{DisplayName: "personal"}, "t1")
	if err != nil || meta.Mode != "" {
		t.Fatalf("got %+v, %v", meta, err)
	}
	p, err = PlanFor(AuthSession{Broker: &scriptBroker{kind: credential.KindOIDC}, UserID: "alice"}, meta.ID)
	if err != nil || p.Subject.Scope != credential.ScopeUser || p.Subject.OwnerID != "alice" {
		t.Errorf("got %+v, %v", p.Subject, err)
	}
}

func TestAddClusterRejectsSharedAccountWhereItMakesNoSense(t *testing.T) {
	memStoreWithHome(t)

	if _, err := AddClusterFromTextToTeam(execKubeconfig, ClusterSettings{DisplayName: "no team", SharedAccount: true}, ""); !errors.Is(err, errSharedNeedsTeam) {
		t.Errorf("without a team: %v", err)
	}
	if _, err := AddClusterFromTextToTeam(sampleKubeconfig, ClusterSettings{DisplayName: "static", SharedAccount: true}, "t1"); !errors.Is(err, errSharedNeedsSignIn) {
		t.Errorf("static credentials: %v", err)
	}
}

func TestUpdateClusterSwitchesSharedAccount(t *testing.T) {
	memStoreWithHome(t)
	meta, err := AddClusterFromTextToTeam(execKubeconfig, ClusterSettings{DisplayName: "c"}, "t1")
	if err != nil {
		t.Fatal(err)
	}

	res, err := UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "c", SharedAccount: true}, KubeconfigSource{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cluster.Mode != ModeTeamAccount || !res.AccountModeChanged || !res.ConnectionChanged {
		t.Fatalf("switching on: %+v", res)
	}
	if got, _ := LoadClusterMeta(meta.ID); got == nil || got.Mode != ModeTeamAccount {
		t.Fatalf("the mode must be stored: %+v", got)
	}

	// Renaming with the flag still on changes neither the mode nor the connection.
	res, err = UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "renamed", SharedAccount: true}, KubeconfigSource{})
	if err != nil || res.AccountModeChanged || res.ConnectionChanged {
		t.Fatalf("renaming: %+v %v", res, err)
	}

	res, err = UpdateClusterSettings(meta.ID, ClusterSettings{DisplayName: "renamed"}, KubeconfigSource{})
	if err != nil || res.Cluster.Mode != "" || !res.AccountModeChanged {
		t.Fatalf("switching off: %+v %v", res, err)
	}
}

// Without a broker only clusters that need no sign-in connect: an exec block is never run.
func TestConnectionWithoutBroker(t *testing.T) {
	memStoreWithHome(t)
	static, err := AddClusterFromText(sampleKubeconfig, ClusterSettings{DisplayName: "static", ContextName: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := PlanFor(AuthSession{}, static.ID); err != nil {
		t.Fatal(err)
	} else if _, err := ConfigFor(p); err != nil {
		t.Errorf("static credentials need no broker: %v", err)
	}

	exec, err := AddClusterFromText(execKubeconfig, ClusterSettings{DisplayName: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanFor(AuthSession{}, exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigFor(p); !errors.Is(err, errNoBroker) {
		t.Errorf("an exec block must not be run without a broker: %v", err)
	}
}

func TestUnknownConnectionModeIsRejected(t *testing.T) {
	memStoreWithHome(t)
	if err := clusterStore.Save(&ClusterMeta{ID: "x", DisplayName: "x", Mode: "mystery"}, []byte(sampleKubeconfig)); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanFor(AuthSession{}, "x"); err == nil {
		t.Fatal("an unknown mode must not be treated as a plain kubeconfig")
	}
}
