// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/identity"
	"github.com/24DSO-lab/kubron/internal/identity/identitytest"
)

func TestFileBackend_Contract(t *testing.T) {
	identitytest.Run(t, func(t *testing.T) identitytest.Harness {
		dir := t.TempDir()
		b, err := identity.OpenFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		return identitytest.Harness{
			Backend: b,
			Reopen: func(t *testing.T) identity.Backend {
				nb, err := identity.OpenFile(dir)
				if err != nil {
					t.Fatalf("reopen: %v", err)
				}
				return nb
			},
		}
	})
}
