// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package autonomy

import (
	"reflect"
	"testing"
)

func TestEmptyAllowedActionIntersectionRemainsDenyAll(t *testing.T) {
	in := baseInput()
	in.Policies[0].Constraints.AllowedActions = []ActionRef{}
	action := ActionRef{ID: "scale", Version: 1}
	in.Action = &action
	in.Mutating = true
	in.RiskKnown = true
	in.Risk = 1

	d := Evaluate(in)
	if d.Allowed || d.MutatingAllowed {
		t.Fatalf("an explicitly empty allowlist must deny every action: %+v", d)
	}
	if d.Effective.AllowedActions == nil {
		t.Fatalf("empty allowlist lost its restrictive meaning: %+v", d.Effective)
	}
	if !containsString(d.ReasonCodes, "action_not_allowed") {
		t.Fatalf("expected action_not_allowed, got %v", d.ReasonCodes)
	}
}

func TestMutatingDecisionRequiresExactActionIDAndVersion(t *testing.T) {
	in := baseInput()
	action := ActionRef{ID: "scale", Version: 7}
	in.Policies[0].Constraints.AllowedActions = []ActionRef{action}
	in.Profiles[0].Constraints.AllowedActions = []ActionRef{action}
	in.Action = &action
	in.Mutating = true
	in.RiskKnown = true
	in.Risk = 1

	if d := Evaluate(in); !d.MutatingAllowed {
		t.Fatalf("exact typed action should satisfy the policy action constraint: %+v", d)
	}

	mismatch := ActionRef{ID: "scale", Version: 8}
	in.Action = &mismatch
	if d := Evaluate(in); d.MutatingAllowed || !containsString(d.ReasonCodes, "action_not_allowed") {
		t.Fatalf("different action version must be denied: %+v", d)
	}
}

func TestFailClosedForUnavailableAmbiguousAndStaleSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
		reason string
	}{
		{"policy unavailable", func(in *Input) { in.PolicySource = SourceUnavailable }, "policy_unavailable"},
		{"policy ambiguous", func(in *Input) { in.PolicySource = SourceAmbiguous }, "policy_unavailable"},
		{"policy stale", func(in *Input) { in.PolicySource = SourceStale }, "policy_stale_revision"},
		{"profile unavailable", func(in *Input) { in.ProfileSource = SourceUnavailable }, "policy_unavailable"},
		{"profile ambiguous", func(in *Input) { in.ProfileSource = SourceAmbiguous }, "binding_ambiguous"},
		{"profile stale", func(in *Input) { in.ProfileSource = SourceStale }, "policy_stale_revision"},
		{"binding unavailable", func(in *Input) { in.BindingSource = SourceUnavailable }, "policy_unavailable"},
		{"binding ambiguous", func(in *Input) { in.BindingSource = SourceAmbiguous }, "binding_ambiguous"},
		{"binding stale", func(in *Input) { in.BindingSource = SourceStale }, "policy_stale_revision"},
		{"missing profile", func(in *Input) { in.ProfileSource = SourceMissing; in.Profiles = nil }, "policy_missing"},
		{"missing binding", func(in *Input) { in.BindingSource = SourceMissing; in.Bindings = nil }, "policy_missing"},
		{"target group missing", func(in *Input) { in.TargetGroupID = "group"; in.TargetGroupSource = SourceMissing }, "fact_unknown"},
		{"target group unavailable", func(in *Input) { in.TargetGroupID = "group"; in.TargetGroupSource = SourceUnavailable }, "policy_unavailable"},
		{"target group ambiguous", func(in *Input) { in.TargetGroupID = "group"; in.TargetGroupSource = SourceAmbiguous }, "fact_unknown"},
		{"target group stale", func(in *Input) { in.TargetGroupID = "group"; in.TargetGroupSource = SourceStale }, "policy_stale_revision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInput()
			tt.mutate(&in)
			d := Evaluate(in)
			if d.Mode != Observe || d.Allowed || d.MutatingAllowed {
				t.Fatalf("input must fail closed: %+v", d)
			}
			if !containsString(d.ReasonCodes, tt.reason) {
				t.Fatalf("expected reason %q, got %v", tt.reason, d.ReasonCodes)
			}
		})
	}
}

func TestEvaluatorOutputIsDeterministicAcrossAllAggregatePermutations(t *testing.T) {
	in := baseInput()
	p2 := in.Policies[0]
	p2.ID = "policy-2"
	p2.Revision = 2
	p2.Constraints.MaxRisk = 50
	profile2 := in.Profiles[0]
	profile2.ID = "profile-2"
	profile2.Revision = 2
	profile2.Constraints.MaxRisk = 60
	binding2 := in.Bindings[0]
	binding2.ID = "binding-2"
	binding2.ProfileID = profile2.ID
	binding2.Revision = 2
	in.Policies = append(in.Policies, p2)
	in.Profiles = append(in.Profiles, profile2)
	in.Bindings = append(in.Bindings, binding2)

	expected := Evaluate(in)
	in.Policies[0], in.Policies[1] = in.Policies[1], in.Policies[0]
	in.Profiles[0], in.Profiles[1] = in.Profiles[1], in.Profiles[0]
	in.Bindings[0], in.Bindings[1] = in.Bindings[1], in.Bindings[0]
	actual := Evaluate(in)
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("aggregate order changed the full decision:\nexpected: %#v\nactual: %#v", expected, actual)
	}
}

func TestDuplicateAggregateIdentityFailsClosed(t *testing.T) {
	in := baseInput()
	duplicate := in.Profiles[0]
	duplicate.Revision = 2
	duplicate.Constraints.ModeCeiling = Observe
	in.Profiles = append(in.Profiles, duplicate)

	d := Evaluate(in)
	if d.Mode != Observe || d.Allowed || !containsString(d.ReasonCodes, "binding_ambiguous") {
		t.Fatalf("duplicate profile identity must be rejected as ambiguous: %+v", d)
	}
}

func TestCrossTenantPolicyCannotApply(t *testing.T) {
	in := baseInput()
	in.TargetOwner = OwnerScope{Kind: OwnerOrganization, OrganizationID: "other-org"}
	d := Evaluate(in)
	if d.Mode != Observe || d.Allowed || !containsString(d.ReasonCodes, "fact_unknown") {
		t.Fatalf("cross-tenant policy input must fail closed: %+v", d)
	}
}

func TestUnknownScopeFactsFailClosed(t *testing.T) {
	in := baseInput()
	in.ScopeFactsKnown = false
	d := Evaluate(in)
	if d.Mode != Observe || d.Allowed || !containsString(d.UnknownInputs, "scope_facts") {
		t.Fatalf("unknown scope facts must fail closed: %+v", d)
	}
}
