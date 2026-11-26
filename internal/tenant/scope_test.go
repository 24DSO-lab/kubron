// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package tenant

import "testing"

func TestScopeValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    Scope
		want bool
	}{
		{"full", Scope{OrgID: "o", TeamID: "t", ClusterID: "c"}, true},
		{"zero", Scope{}, false},
		{"no org", Scope{TeamID: "t", ClusterID: "c"}, false},
		{"no team", Scope{OrgID: "o", ClusterID: "c"}, false},
		{"no cluster", Scope{OrgID: "o", TeamID: "t"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.s.Valid(); got != tc.want {
				t.Fatalf("Valid() = %v, want %v", got, tc.want)
			}
		})
	}
}
