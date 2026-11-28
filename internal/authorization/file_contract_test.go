// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/authorization"
	"github.com/24DSO-lab/kubron/internal/authorization/authorizationtest"
)

func TestFileBackend_Contract(t *testing.T) {
	authorizationtest.Run(t, func(t *testing.T) authorizationtest.Harness {
		dir := t.TempDir()
		b, err := authorization.OpenFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		return authorizationtest.Harness{
			Grants: b, Delegations: b,
			Reopen: func(t *testing.T) (authorization.GrantBackend, authorization.DelegationBackend) {
				nb, err := authorization.OpenFile(dir)
				if err != nil {
					t.Fatalf("reopen: %v", err)
				}
				return nb, nb
			},
		}
	})
}
