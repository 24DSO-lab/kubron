// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/auth"
)

// RPC пересчёта корреляции: кластер, которого нет в каталоге, неотличим от чужого.
func TestIncidentCorrelationRecomputeUnknownCluster(t *testing.T) {
	e := newE2EApp(t)
	if _, err := e.app.IncidentCorrelationRecompute("nope", "inc-a"); !errors.Is(err, auth.ErrClusterNotFound) {
		t.Fatalf("got %v, want ErrClusterNotFound", err)
	}
}

// Пользователь только с правом чтения пересчитывать не может: нужно operate.
func TestIncidentCorrelationRecomputeRequiresOperate(t *testing.T) {
	e := newE2EApp(t)
	e.seed(t, "inc-a", "web")
	if _, err := e.app.auth.CreateUser("bob", testPass, auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	e.app.auth.Logout()
	if _, err := e.app.auth.Login(t.Context(), "bob", testPass); err != nil {
		t.Fatal(err)
	}
	if err := e.app.auth.ChangePassword(t.Context(), testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.IncidentCorrelationRecompute(e2eCluster, "inc-a"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

func TestIncidentCorrelationRecomputeUnavailableStorage(t *testing.T) {
	e := newE2EApp(t)
	e.app.incidents = nil
	if _, err := e.app.IncidentCorrelationRecompute(e2eCluster, "inc-a"); !errors.Is(err, errIncidentsUnavailable) {
		t.Fatalf("got %v, want errIncidentsUnavailable", err)
	}
}
