// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package autonomy

import (
	"errors"
	"testing"
	"time"
)

func TestCASLifecycleAndTombstone(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	owner := OwnerScope{Kind: OwnerOrganization, OrganizationID: "org"}
	scope := ScopeRef{Kind: ScopeOrganization, OrganizationID: "org"}
	prov, _ := NewProvenance("user", "actor", now.Add(-time.Hour))
	p := Policy{ID: "p", Owner: owner, Scope: scope, Constraints: Constraints{ModeCeiling: Assisted, MaxRisk: 40}, Revision: 1, Lifecycle: Active, Provenance: prov}
	meta := MutationMeta{ExpectedRevision: 1, ActorKind: "user", ActorID: "actor", At: now}
	next := p
	next.Constraints.MaxRisk = 20
	updated, err := ApplyPolicyUpdate(p, next, meta)
	if err != nil || updated.Revision != 2 || updated.Constraints.MaxRisk != 20 {
		t.Fatalf("update failed: %+v %v", updated, err)
	}
	if _, err = ApplyPolicyUpdate(updated, next, meta); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS must conflict: %v", err)
	}
	meta.ExpectedRevision = 2
	deactivated, err := DeactivatePolicy(updated, meta)
	if err != nil || deactivated.Lifecycle != Deactivated || deactivated.Revision != 3 {
		t.Fatalf("tombstone failed: %+v %v", deactivated, err)
	}
}
func TestOwnerScopeCannotChange(t *testing.T) {
	now := time.Now().UTC()
	org := OwnerScope{Kind: OwnerOrganization, OrganizationID: "org"}
	prov, _ := NewProvenance("user", "actor", now)
	p := Policy{ID: "p", Owner: org, Scope: ScopeRef{Kind: ScopeOrganization, OrganizationID: "org"}, Constraints: Constraints{ModeCeiling: Assisted, MaxRisk: 20}, Revision: 1, Lifecycle: Active, Provenance: prov}
	next := p
	next.Owner = OwnerScope{Kind: OwnerOrganization, OrganizationID: "other"}
	_, err := ApplyPolicyUpdate(p, next, MutationMeta{ExpectedRevision: 1, ActorKind: "user", ActorID: "actor", At: now})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner change should be rejected: %v", err)
	}
}
func TestTeamProfileCannotBindAcrossTeams(t *testing.T) {
	now := time.Now().UTC()
	owner := OwnerScope{Kind: OwnerTeam, OrganizationID: "org", TeamID: "team-a"}
	prov, _ := NewProvenance("user", "actor", now)
	profile := Profile{ID: "profile", Owner: owner, Constraints: Constraints{ModeCeiling: Observe, MaxRisk: 0}, Revision: 1, Lifecycle: Active, Provenance: prov}
	binding := Binding{ID: "binding", ProfileID: "profile", Owner: owner, Scope: ScopeRef{Kind: ScopeCluster, OrganizationID: "org", TeamID: "team-b", ClusterID: "cluster"}, Revision: 1, Lifecycle: Active, Provenance: prov}
	if err := ValidateBindingProfile(binding, profile); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-team binding must be rejected: %v", err)
	}
}
