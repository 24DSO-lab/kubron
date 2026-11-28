// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package autonomy

import (
	"sort"
	"time"
)

type SourceState string
const (
	SourceAvailable SourceState = "available"
	SourceMissing SourceState = "missing"
	SourceUnavailable SourceState = "unavailable"
	SourceAmbiguous SourceState = "ambiguous"
	SourceStale SourceState = "stale"
)

type Revision struct { ID string; Revision uint64 }
type RevisionVector struct {
	Policies []Revision
	Profiles []Revision
	Bindings []Revision
	TargetGroupID string
	TargetGroupRevision uint64
}
type Input struct {
	At time.Time
	Scope ScopeRef
	TargetOwner OwnerScope
	ScopeFactsKnown bool
	Policies []Policy
	Profiles []Profile
	Bindings []Binding
	PolicySource SourceState
	ProfileSource SourceState
	BindingSource SourceState
	TargetGroupSource SourceState
	Action *ActionRef
	Mutating bool
	Risk uint8
	RiskKnown bool
	ApprovalFactsKnown bool
	SatisfiedApprovals []string
	BudgetFactsKnown bool
	BudgetAvailable bool
	TargetGroupID string
	TargetGroupRevision uint64
}
type EffectiveConstraints struct {
	Mode Mode
	AllowedActions []ActionRef
	DeniedActions []ActionRef
	MaxRisk uint8
	RequiredApprovals []string
	Budgets []Budget
	AllowedTimeWindows []TimeWindow
	RequireVerification bool
	RequireRecovery bool
	RequireAudit bool
}
type Decision struct {
	Mode Mode
	Allowed bool
	MutatingAllowed bool
	Effective EffectiveConstraints
	ReasonCodes []string
	UnknownInputs []string
	RevisionVector RevisionVector
}

func Evaluate(in Input) Decision {
	d := Decision{Mode: Observe, Effective: EffectiveConstraints{Mode: Observe, MaxRisk: 0}}
	addReason := func(code string) { d.ReasonCodes = append(d.ReasonCodes, code) }
	addUnknown := func(name string) { d.UnknownInputs = append(d.UnknownInputs, name) }
	if in.At.IsZero() || in.Scope.Validate() != nil || in.TargetOwner.Validate() != nil || !in.ScopeFactsKnown {
		addReason("fact_unknown"); addUnknown("scope_facts"); return finalize(d)
	}
	if in.PolicySource == SourceStale {
		addReason("policy_stale_revision"); addUnknown("policies"); return finalize(d)
	}
	if in.PolicySource != SourceAvailable || len(in.Policies) == 0 {
		switch in.PolicySource {
		case SourceUnavailable, SourceAmbiguous: addReason("policy_unavailable"); addUnknown("policies")
		default: addReason("policy_missing"); addUnknown("policies")
		}
		return finalize(d)
	}
	if in.ProfileSource == SourceUnavailable || in.BindingSource == SourceUnavailable || in.TargetGroupSource == SourceUnavailable {
		addReason("policy_unavailable"); addUnknown("profile_or_binding_or_target_group"); return finalize(d)
	}
	if in.ProfileSource == SourceMissing && len(in.Profiles) > 0 || in.BindingSource == SourceMissing && len(in.Bindings) > 0 {
		addReason("binding_ambiguous"); addUnknown("profile_binding_state"); return finalize(d)
	}
	if in.ProfileSource == SourceAmbiguous || in.BindingSource == SourceAmbiguous {
		addReason("binding_ambiguous"); addUnknown("bindings"); return finalize(d)
	}
	if in.ProfileSource == SourceStale || in.BindingSource == SourceStale || in.TargetGroupSource == SourceStale {
		addReason("policy_stale_revision"); addUnknown("revision_vector"); return finalize(d)
	}
	if len(in.Profiles) == 0 || len(in.Bindings) == 0 {
		addReason("policy_missing"); addUnknown("profile_binding"); return finalize(d)
	}
	for _, p := range in.Policies {
		if p.Validate() != nil || p.Lifecycle != Active || !ownerApplies(p.Owner, in.TargetOwner) || !scopeApplies(p.Scope, in.Scope) {
			addReason("fact_unknown"); addUnknown("policy_scope_or_record"); return finalize(d)
		}
		d.RevisionVector.Policies = append(d.RevisionVector.Policies, Revision{p.ID,p.Revision})
	}
	for _, b := range in.Bindings {
		if b.Validate() != nil || b.Lifecycle != Active || !ownerApplies(b.Owner, in.TargetOwner) {
			addReason("binding_ambiguous"); addUnknown("binding_record"); return finalize(d)
		}
		profile, ok := findProfile(in.Profiles, b.ProfileID)
		if !ok || profile.Lifecycle != Active || profile.Validate() != nil {
			addReason("policy_missing"); addUnknown("bound_profile"); return finalize(d)
		}
		if !compatibleOwner(profile.Owner, b.Owner, in.TargetOwner) || !scopeApplies(b.Scope, in.Scope) {
			addReason("binding_ambiguous"); addUnknown("binding_scope"); return finalize(d)
		}
		d.RevisionVector.Bindings = append(d.RevisionVector.Bindings, Revision{b.ID,b.Revision})
		d.RevisionVector.Profiles = append(d.RevisionVector.Profiles, Revision{profile.ID,profile.Revision})
	}
	// Every supplied profile must be referenced by an applicable binding; otherwise its application is ambiguous.
	for _, p := range in.Profiles {
		found := false
		for _, b := range in.Bindings { if b.ProfileID == p.ID && b.Lifecycle == Active { found = true; break } }
		if !found { addReason("binding_ambiguous"); addUnknown("unbound_profile"); return finalize(d) }
	}
	if in.TargetGroupID != "" {
		if in.TargetGroupSource != SourceAvailable || in.TargetGroupRevision == 0 {
			addReason("fact_unknown"); addUnknown("target_group_revision"); return finalize(d)
		}
		d.RevisionVector.TargetGroupID, d.RevisionVector.TargetGroupRevision = in.TargetGroupID, in.TargetGroupRevision
	}
	all := make([]Constraints,0,len(in.Policies)+len(in.Profiles))
	for _, p := range in.Policies { all = append(all,p.Constraints) }
	for _, p := range in.Profiles { all = append(all,p.Constraints) }
	effective, conflict := intersect(all)
	if conflict { addReason("constraints_conflict"); return finalize(d) }
	d.Effective = effective
	d.Mode = effective.Mode
	if in.Mutating && !in.RiskKnown { addReason("risk_exceeded"); addUnknown("action_risk") } else if in.RiskKnown && in.Risk > effective.MaxRisk { addReason("risk_exceeded") }
	if in.Mutating && in.Action == nil { addReason("action_not_allowed"); addUnknown("exact_action_reference") }
	if in.Action != nil {
		if in.Action.Validate() != nil || (in.Scope.Kind == ScopeAction && (in.Scope.ActionID != in.Action.ID || in.Scope.ActionVersion != in.Action.Version)) { addReason("action_not_allowed"); addUnknown("action_reference") } else {
			for _, a := range effective.DeniedActions { if a.Equal(*in.Action) { addReason("action_not_allowed") } }
			if effective.AllowedActions != nil && !containsAction(effective.AllowedActions,*in.Action) { addReason("action_not_allowed") }
		}
	}
	if len(effective.RequiredApprovals)>0 {
		if !in.ApprovalFactsKnown { addReason("approval_required"); addUnknown("approval_facts") } else { for _, required := range effective.RequiredApprovals { if !containsString(in.SatisfiedApprovals,required) { addReason("approval_required"); break } } }
	}
	if len(effective.Budgets)>0 { if !in.BudgetFactsKnown { addReason("budget_exceeded"); addUnknown("budget_facts") } else if !in.BudgetAvailable { addReason("budget_exceeded") } }
	if !withinWindows(in.At,effective.AllowedTimeWindows) { addReason("time_window_closed") }
	if in.Mutating && (d.Mode == Observe || d.Mode == "") { addReason("mode_restricted") }
	d.Allowed = len(d.ReasonCodes)==0
	d.MutatingAllowed = d.Allowed && in.Mutating && d.Mode != Observe && in.Action != nil && in.Action.Validate()==nil
	return finalize(d)
}

func intersect(all []Constraints) (EffectiveConstraints,bool) {
	e := EffectiveConstraints{Mode:Autonomous,MaxRisk:100}
	var allowedSet map[ActionRef]bool
	for _, c := range all {
		if c.Validate()!=nil { return EffectiveConstraints{Mode:Observe},true }
		if modeRank(c.ModeCeiling)<modeRank(e.Mode) { e.Mode=c.ModeCeiling }
		if c.MaxRisk<e.MaxRisk { e.MaxRisk=c.MaxRisk }
		if c.AllowedActions!=nil {
			next:=map[ActionRef]bool{}; for _,a:=range c.AllowedActions { next[a]=true }
			if allowedSet==nil { allowedSet=next } else { for a:=range allowedSet { if !next[a] { delete(allowedSet,a) } } }
		}
		e.DeniedActions=append(e.DeniedActions,c.DeniedActions...)
		e.RequiredApprovals=append(e.RequiredApprovals,c.RequiredApprovals...)
		e.Budgets=append(e.Budgets,c.Budgets...)
		e.RequireVerification=e.RequireVerification||c.RequireVerification
		e.RequireRecovery=e.RequireRecovery||c.RequireRecovery
		e.RequireAudit=e.RequireAudit||c.RequireAudit
		if len(c.AllowedTimeWindows)>0 {
			if len(e.AllowedTimeWindows)==0 { e.AllowedTimeWindows=cloneWindows(c.AllowedTimeWindows) } else { e.AllowedTimeWindows=intersectWindows(e.AllowedTimeWindows,c.AllowedTimeWindows) }
			if len(e.AllowedTimeWindows)==0 { return EffectiveConstraints{Mode:Observe},true }
		}
	}
	if allowedSet!=nil {
		for a:=range allowedSet { e.AllowedActions=append(e.AllowedActions,a) }
		sort.Slice(e.AllowedActions,func(i,j int)bool { if e.AllowedActions[i].ID==e.AllowedActions[j].ID { return e.AllowedActions[i].Version<e.AllowedActions[j].Version }; return e.AllowedActions[i].ID<e.AllowedActions[j].ID })
	}
	e.DeniedActions=uniqueActions(e.DeniedActions); e.RequiredApprovals=uniqueStrings(e.RequiredApprovals)
	return e,false
}
func modeRank(m Mode) int { switch m { case Observe:return 0; case Assisted:return 1; case Autonomous:return 2 }; return 0 }
func scopeApplies(binding, target ScopeRef) bool {
	if binding.OrganizationID!=target.OrganizationID { return false }
	if binding.TeamID!="" && binding.TeamID!=target.TeamID { return false }
	switch binding.Kind {
	case ScopeOrganization:return true
	case ScopeCluster:return binding.ClusterID==target.ClusterID
	case ScopeNamespace:return binding.ClusterID==target.ClusterID && binding.Namespace==target.Namespace
	case ScopeWorkload:return binding.ClusterID==target.ClusterID && binding.Namespace==target.Namespace && binding.Workload==target.Workload
	case ScopeObject:return binding.ClusterID==target.ClusterID && binding.ObjectUID==target.ObjectUID
	case ScopeAction:return binding.ClusterID==target.ClusterID && binding.ObjectUID==target.ObjectUID && binding.ActionID==target.ActionID && binding.ActionVersion==target.ActionVersion && inActionScopeMatches(binding,target)
	case ScopeTargetGroup:return binding.TargetGroupID==target.TargetGroupID
	}
	return false
}
func inActionScopeMatches(binding,target ScopeRef) bool { return binding.ActionID == target.ActionID && binding.ActionVersion == target.ActionVersion }
func ownerApplies(owner,target OwnerScope) bool {
	if owner.OrganizationID != target.OrganizationID { return false }
	if owner.Kind == OwnerOrganization { return owner.TeamID == "" }
	return owner.Kind == OwnerTeam && target.Kind == OwnerTeam && owner.TeamID == target.TeamID
}
func compatibleOwner(profile,binding,target OwnerScope) bool {
	if profile!=binding || profile.OrganizationID!=target.OrganizationID { return false }
	if profile.Kind==OwnerOrganization { return profile.TeamID=="" && target.Kind==OwnerOrganization || profile.TeamID=="" && target.Kind==OwnerTeam }
	return profile.Kind==OwnerTeam && target.Kind==OwnerTeam && profile.TeamID==target.TeamID
}
func findProfile(ps []Profile,id string)(Profile,bool){ for _,p:=range ps { if p.ID==id{return p,true} }; return Profile{},false }
func containsString(xs []string,s string)bool{for _,x:=range xs{if x==s{return true}};return false}
func containsAction(xs []ActionRef,a ActionRef)bool{for _,x:=range xs{if x.Equal(a){return true}};return false}
func uniqueActions(xs []ActionRef)[]ActionRef{m:=map[ActionRef]bool{};out:=[]ActionRef{};for _,x:=range xs{if !m[x]{m[x]=true;out=append(out,x)}};sort.Slice(out,func(i,j int)bool{if out[i].ID==out[j].ID{return out[i].Version<out[j].Version};return out[i].ID<out[j].ID});return out}
func uniqueStrings(xs []string)[]string{m:=map[string]bool{};out:=[]string{};for _,x:=range xs{if !m[x]{m[x]=true;out=append(out,x)}};sort.Strings(out);return out}
func cloneWindows(xs []TimeWindow)[]TimeWindow{out:=make([]TimeWindow,len(xs));copy(out,xs);return out}
func intersectWindows(a,b []TimeWindow)[]TimeWindow{
	out:=[]TimeWindow{}
	for _,x:=range a{for _,y:=range b{
		start:=x.StartMinute;if y.StartMinute>start{start=y.StartMinute};end:=x.EndMinute;if y.EndMinute<end{end=y.EndMinute}
		if start>=end{continue}
		days:=[]time.Weekday{}; for _,dx:=range x.Days{for _,dy:=range y.Days{if dx==dy{days=append(days,dx)}}}
		if len(x.Days)==0{days=append(days,y.Days...)};if len(y.Days)==0{days=append(days,x.Days...)}
		if len(x.Days)==0&&len(y.Days)==0 || len(days)>0 { out=append(out,TimeWindow{StartMinute:start,EndMinute:end,Days:days}) }
	}}
	return out
}
func withinWindows(at time.Time,ws []TimeWindow)bool{
	if len(ws)==0{return true};minute:=at.Hour()*60+at.Minute()
	for _,w:=range ws{if minute>=w.StartMinute&&minute<w.EndMinute{if len(w.Days)==0{return true};for _,d:=range w.Days{if d==at.Weekday(){return true}}}}
	return false
}
func finalize(d Decision)Decision{
	d.ReasonCodes=uniqueStrings(d.ReasonCodes);d.UnknownInputs=uniqueStrings(d.UnknownInputs)
	sort.Slice(d.Effective.Budgets,func(i,j int)bool { a,b:=d.Effective.Budgets[i],d.Effective.Budgets[j]; if a.Period!=b.Period{return a.Period<b.Period};if a.MaxExecutions!=b.MaxExecutions{return a.MaxExecutions<b.MaxExecutions};return a.MaxConcurrent<b.MaxConcurrent })
	sort.Slice(d.Effective.AllowedTimeWindows,func(i,j int)bool { a,b:=d.Effective.AllowedTimeWindows[i],d.Effective.AllowedTimeWindows[j];if a.StartMinute!=b.StartMinute{return a.StartMinute<b.StartMinute};return a.EndMinute<b.EndMinute })
	for i:=range d.Effective.AllowedTimeWindows { sort.Slice(d.Effective.AllowedTimeWindows[i].Days,func(a,b int)bool{return d.Effective.AllowedTimeWindows[i].Days[a]<d.Effective.AllowedTimeWindows[i].Days[b]}) }
	sort.Slice(d.RevisionVector.Policies,func(i,j int)bool{return d.RevisionVector.Policies[i].ID<d.RevisionVector.Policies[j].ID})
	sort.Slice(d.RevisionVector.Profiles,func(i,j int)bool{return d.RevisionVector.Profiles[i].ID<d.RevisionVector.Profiles[j].ID})
	sort.Slice(d.RevisionVector.Bindings,func(i,j int)bool{return d.RevisionVector.Bindings[i].ID<d.RevisionVector.Bindings[j].ID})
	return d
}
