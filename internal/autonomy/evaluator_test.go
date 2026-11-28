// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package autonomy

import (
	"testing"
)

func TestObservePreviewIsAllowedButNeverMutating(t *testing.T) {
	in:=baseInput()
	in.Policies[0].Constraints.ModeCeiling=Observe
	preview:=Evaluate(in)
	if !preview.Allowed || preview.MutatingAllowed { t.Fatalf("observe preview should be allowed and non-mutating: %+v",preview) }
	action:=ActionRef{ID:"scale",Version:1}
	in.Action=&action;in.Mutating=true
	mutation:=Evaluate(in)
	if mutation.Allowed || mutation.MutatingAllowed { t.Fatalf("observe must deny mutation: %+v",mutation) }
}
func TestApprovalRequirementNeedsMatchingEvidence(t *testing.T) {
	in:=baseInput()
	in.Policies[0].Constraints.RequiredApprovals=[]string{"change-approval"}
	d:=Evaluate(in)
	if d.Allowed || !containsString(d.ReasonCodes,"approval_required") { t.Fatalf("missing approval was accepted: %+v",d) }
	in.SatisfiedApprovals=[]string{"change-approval"}
	d=Evaluate(in)
	if !d.Allowed { t.Fatalf("matching approval evidence was rejected: %+v",d) }
}
func TestStalePolicySourceHasStableReason(t *testing.T) {
	in:=baseInput();in.PolicySource=SourceStale
	d:=Evaluate(in)
	if d.Mode!=Observe || !containsString(d.ReasonCodes,"policy_stale_revision") { t.Fatalf("stale policy did not fail closed: %+v",d) }
}
func TestActionScopeRequiresExactVersion(t *testing.T) {
	base:=ScopeRef{Kind:ScopeAction,OrganizationID:"org",ClusterID:"cluster",ObjectUID:"uid",ActionID:"scale",ActionVersion:2}
	target:=base
	if !scopeApplies(base,target) { t.Fatal("exact action scope should match") }
	target.ActionVersion=1
	if scopeApplies(base,target) { t.Fatal("different action version must not match") }
}
func TestTimeWindowRejectsOvernightAmbiguity(t *testing.T) {
	if (TimeWindow{StartMinute:22*60,EndMinute:6*60}).Validate()==nil { t.Fatal("overnight window must be represented explicitly as bounded windows") }
}
