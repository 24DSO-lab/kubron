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

func TestProfileAndBindingRevisionCASRaces(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	owner := OwnerScope{Kind: OwnerOrganization, OrganizationID: "org"}
	scope := ScopeRef{Kind: ScopeOrganization, OrganizationID: "org"}
	prov, _ := NewProvenance("user", "actor", now.Add(-time.Hour))
	meta := MutationMeta{ExpectedRevision: 1, ActorKind: "user", ActorID: "actor", At: now}

	profile := Profile{ID: "profile", Owner: owner, Constraints: Constraints{ModeCeiling: Assisted, MaxRisk: 40}, Revision: 1, Lifecycle: Active, Provenance: prov}
	nextProfile := profile
	nextProfile.Constraints.MaxRisk = 20
	updatedProfile, err := ApplyProfileUpdate(profile, nextProfile, meta)
	if err != nil || updatedProfile.Revision != 2 {
		t.Fatalf("profile update failed: %+v %v", updatedProfile, err)
	}
	if _, err := ApplyProfileUpdate(updatedProfile, nextProfile, meta); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale profile revision must conflict: %v", err)
	}

	binding := Binding{ID: "binding", ProfileID: profile.ID, Owner: owner, Scope: scope, Revision: 1, Lifecycle: Active, Provenance: prov}
	nextBinding := binding
	nextBinding.Scope = ScopeRef{Kind: ScopeCluster, OrganizationID: "org", ClusterID: "cluster"}
	updatedBinding, err := ApplyBindingUpdate(binding, nextBinding, meta)
	if err != nil || updatedBinding.Revision != 2 || updatedBinding.Scope.ClusterID != "cluster" {
		t.Fatalf("binding update failed: %+v %v", updatedBinding, err)
	}
	if _, err := ApplyBindingUpdate(updatedBinding, nextBinding, meta); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale binding revision must conflict: %v", err)
	}
}

func TestProfileAndBindingDeactivationAreRevisionedTombstones(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	owner := OwnerScope{Kind: OwnerOrganization, OrganizationID: "org"}
	prov, _ := NewProvenance("user", "actor", now.Add(-time.Hour))
	meta := MutationMeta{ExpectedRevision: 1, ActorKind: "user", ActorID: "actor", At: now}
	profile := Profile{ID: "profile", Owner: owner, Constraints: Constraints{ModeCeiling: Observe, MaxRisk: 0}, Revision: 1, Lifecycle: Active, Provenance: prov}
	binding := Binding{ID: "binding", ProfileID: profile.ID, Owner: owner, Scope: ScopeRef{Kind: ScopeOrganization, OrganizationID: "org"}, Revision: 1, Lifecycle: Active, Provenance: prov}

	deactivatedProfile, err := DeactivateProfile(profile, meta)
	if err != nil || deactivatedProfile.Lifecycle != Deactivated || deactivatedProfile.Revision != 2 {
		t.Fatalf("profile tombstone failed: %+v %v", deactivatedProfile, err)
	}
	deactivatedBinding, err := DeactivateBinding(binding, meta)
	if err != nil || deactivatedBinding.Lifecycle != Deactivated || deactivatedBinding.Revision != 2 {
		t.Fatalf("binding tombstone failed: %+v %v", deactivatedBinding, err)
	}
}

func TestProfileCannotBeSharedAcrossOwnerScopes(t *testing.T) {
	now := time.Now().UTC()
	orgOwner := OwnerScope{Kind: OwnerOrganization, OrganizationID: "org"}
	teamOwner := OwnerScope{Kind: OwnerTeam, OrganizationID: "org", TeamID: "team"}
	prov, _ := NewProvenance("user", "actor", now)
	profile := Profile{ID: "profile", Owner: orgOwner, Constraints: Constraints{ModeCeiling: Observe, MaxRisk: 0}, Revision: 1, Lifecycle: Active, Provenance: prov}
	binding := Binding{ID: "binding", ProfileID: profile.ID, Owner: teamOwner, Scope: ScopeRef{Kind: ScopeCluster, OrganizationID: "org", TeamID: "team", ClusterID: "cluster"}, Revision: 1, Lifecycle: Active, Provenance: prov}
	if err := ValidateBindingProfile(binding, profile); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner profile sharing must be forbidden: %v", err)
	}
}

func TestOrganizationProfileCanBindWithinItsOrganizationTeamScope(t *testing.T) {
	in := baseInput()
	teamOwner := OwnerScope{Kind: OwnerTeam, OrganizationID: "org", TeamID: "team"}
	in.TargetOwner = teamOwner
	in.Scope.TeamID = "team"
	in.Bindings[0].Scope.TeamID = "team"

	decision := Evaluate(in)
	if !decision.Allowed || decision.Mode != Autonomous {
		t.Fatalf("organization-owned profile should apply within its organization team scope: %+v", decision)
	}
}
