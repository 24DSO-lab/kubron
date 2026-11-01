// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package example

import (
	"context"
	"testing"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

func TestExternalModuleRegistersAuthenticator(t *testing.T) {
	r := ext.NewRegistry()
	if err := r.Install(New(map[string]string{"alice": "pw"})); err != nil {
		t.Fatal(err)
	}
	a, ok := r.Authenticator("static")
	if !ok {
		t.Fatal("authenticator is not registered")
	}
	pa, ok := a.(ext.PasswordAuthenticator)
	if !ok {
		t.Fatal("expected PasswordAuthenticator")
	}
	id, err := pa.Authenticate(context.Background(), "alice", "pw")
	if err != nil || id.Username != "alice" {
		t.Fatalf("login: %+v %v", id, err)
	}
	if _, err := pa.Authenticate(context.Background(), "alice", "wrong"); err == nil {
		t.Fatal("a wrong password must be rejected")
	}
}
