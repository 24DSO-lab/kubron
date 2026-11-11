// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// ConnectionFor даёт подключение именно этой сессии и не делает кластер активным.
func TestConnectionForDoesNotActivate(t *testing.T) {
	id := storedCluster(t)
	m, builds, _ := poolFixture(t)
	b := &scriptBroker{kind: credential.KindOIDC}
	alice, bob := viewFor(m, b, "alice"), viewFor(m, b, "bob")

	ca, err := alice.ConnectionFor(id)
	if err != nil || ca == nil {
		t.Fatalf("ConnectionFor: %v", err)
	}
	if alice.GetActiveContext() != "" {
		t.Fatalf("ConnectionFor changed the active cluster to %q", alice.GetActiveContext())
	}
	if again, _ := alice.ConnectionFor(id); again != ca {
		t.Fatal("a second call must reuse the pooled connection")
	}
	cb, err := bob.ConnectionFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if cb == ca {
		t.Fatal("personal credentials: alice and bob must not share a connection")
	}
	if builds.Load() != 2 {
		t.Fatalf("builds = %d, want 2 (one per user)", builds.Load())
	}
}

func TestConnectionForUnknownCluster(t *testing.T) {
	storedCluster(t)
	m, _, _ := poolFixture(t)
	if _, err := m.ConnectionFor("no-such-cluster"); err == nil {
		t.Fatal("an unknown cluster must fail")
	}
}
