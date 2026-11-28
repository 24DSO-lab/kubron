// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package autonomy

import (
	"testing"
	"time"
)

func baseInput() Input {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	owner := OwnerScope{Kind: OwnerOrganization, OrganizationID: "org"}
	scope := ScopeRef{Kind: ScopeCluster, OrganizationID: "org", ClusterID: "cluster"}
	prov := Provenance{ActorKind: "user", ActorID: "actor", CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	p := Policy{ID: "policy", Owner: owner, Scope: ScopeRef{Kind: ScopeOrganization, OrganizationID: "org"}, Constraints: Constraints{ModeCeiling: Autonomous, MaxRisk: 80, RequireAudit: true}, Revision: 1, Lifecycle: Active, Provenance: prov}
	profile := Profile{ID: "profile", Owner: owner, Constraints: Constraints{ModeCeiling: Autonomous, MaxRisk: 80}, Revision: 1, Lifecycle: Active, Provenance: prov}
	binding := Binding{ID: "binding", ProfileID: profile.ID, Owner: owner, Scope: scope, Revision: 1, Lifecycle: Active, Provenance: prov}
	return Input{At: now, Scope: scope, TargetOwner: owner, ScopeFactsKnown: true, Policies: []Policy{p}, Profiles: []Profile{profile}, Bindings: []Binding{binding}, PolicySource: SourceAvailable, ProfileSource: SourceAvailable, BindingSource: SourceAvailable, TargetGroupSource: SourceAvailable, ApprovalFactsKnown: true, BudgetFactsKnown: true, BudgetAvailable: true}
}
func TestEvaluateRequiresExactActionForMutation(t *testing.T) {
	in := baseInput()
	in.Mutating = true
	d := Evaluate(in)
	if d.MutatingAllowed || !containsString(d.ReasonCodes, "action_not_allowed") {
		t.Fatalf("expected fail-closed exact action requirement: %+v", d)
	}
}
func TestEvaluateAllowsScopePreviewWithoutAction(t *testing.T) {
	in := baseInput()
	d := Evaluate(in)
	if !d.Allowed || d.MutatingAllowed || d.Mode != Autonomous {
		t.Fatalf("unexpected preview: %+v", d)
	}
}
func TestPolicyPermutationInvariant(t *testing.T) {
	in := baseInput()
	p := in.Policies[0]
	p.ID = "policy-b"
	p.Constraints.MaxRisk = 60
	in.Policies = append(in.Policies, p)
	a := Evaluate(in)
	in.Policies[0], in.Policies[1] = in.Policies[1], in.Policies[0]
	b := Evaluate(in)
	if a.Mode != b.Mode || a.Effective.MaxRisk != b.Effective.MaxRisk || !equalStrings(a.ReasonCodes, b.ReasonCodes) {
		t.Fatalf("order changed decision: %+v != %+v", a, b)
	}
}
func TestAllowIntersectionAndDenyUnion(t *testing.T) {
	a := ActionRef{ID: "scale", Version: 1}
	b := ActionRef{ID: "restart", Version: 1}
	e, conflict := intersect([]Constraints{{ModeCeiling: Autonomous, MaxRisk: 100, AllowedActions: []ActionRef{a, b}, DeniedActions: []ActionRef{b}}, {ModeCeiling: Assisted, MaxRisk: 50, AllowedActions: []ActionRef{a}}})
	if conflict || e.Mode != Assisted || e.MaxRisk != 50 || len(e.AllowedActions) != 1 || !e.AllowedActions[0].Equal(a) || len(e.DeniedActions) != 1 {
		t.Fatalf("bad intersection: %+v", e)
	}
}
func TestEmptyTimeWindowIntersectionFails(t *testing.T) {
	_, conflict := intersect([]Constraints{{ModeCeiling: Autonomous, MaxRisk: 100, AllowedTimeWindows: []TimeWindow{{StartMinute: 60, EndMinute: 120}}}, {ModeCeiling: Autonomous, MaxRisk: 100, AllowedTimeWindows: []TimeWindow{{StartMinute: 180, EndMinute: 240}}}})
	if !conflict {
		t.Fatal("expected empty window intersection to conflict")
	}
}
func TestMonotonicRestriction(t *testing.T) {
	base, _ := intersect([]Constraints{{ModeCeiling: Autonomous, MaxRisk: 100}})
	restricted, _ := intersect([]Constraints{{ModeCeiling: Autonomous, MaxRisk: 100}, {ModeCeiling: Assisted, MaxRisk: 40, RequireAudit: true}})
	if modeRank(restricted.Mode) > modeRank(base.Mode) || restricted.MaxRisk > base.MaxRisk || !restricted.RequireAudit {
		t.Fatalf("restriction expanded policy: %+v", restricted)
	}
}
func TestMissingPolicyFailsClosed(t *testing.T) {
	in := baseInput()
	in.PolicySource = SourceMissing
	d := Evaluate(in)
	if d.Mode != Observe || d.Allowed || !containsString(d.ReasonCodes, "policy_missing") {
		t.Fatalf("not fail closed: %+v", d)
	}
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
