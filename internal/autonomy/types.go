// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package autonomy defines versioned autonomy constraints and deterministic, fail-closed evaluation.
// It deliberately has no storage, transport, Kubernetes, authorization, or execution dependencies.
package autonomy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalid = errors.New("autonomy: invalid")
	ErrNotFound = errors.New("autonomy: not found")
	ErrConflict = errors.New("autonomy: revision conflict")
	ErrUnavailable = errors.New("autonomy: dependency unavailable")
	ErrForbidden = errors.New("autonomy: scope forbidden")
)

type Mode string
const (
	Observe Mode = "observe"
	Assisted Mode = "assisted"
	Autonomous Mode = "autonomous"
)

type OwnerKind string
const (
	OwnerOrganization OwnerKind = "organization"
	OwnerTeam OwnerKind = "team"
)

// OwnerScope is immutable ownership, not a grant or target authorization.
type OwnerScope struct {
	Kind OwnerKind
	OrganizationID string
	TeamID string
}
func (s OwnerScope) Validate() error {
	if !validID(s.OrganizationID) { return invalid("OwnerScope.OrganizationID") }
	switch s.Kind {
	case OwnerOrganization:
		if s.TeamID != "" { return invalid("OwnerScope.TeamID") }
	case OwnerTeam:
		if !validID(s.TeamID) { return invalid("OwnerScope.TeamID") }
	default: return invalid("OwnerScope.Kind")
	}
	return nil
}
func (s OwnerScope) Equal(other OwnerScope) bool { return s == other }

type ScopeKind string
const (
	ScopeOrganization ScopeKind = "organization"
	ScopeCluster ScopeKind = "cluster"
	ScopeNamespace ScopeKind = "namespace"
	ScopeWorkload ScopeKind = "workload"
	ScopeObject ScopeKind = "object"
	ScopeAction ScopeKind = "action"
	ScopeTargetGroup ScopeKind = "target_group"
)

// ScopeRef identifies where a policy or profile binding applies.
type ScopeRef struct {
	Kind ScopeKind
	OrganizationID string
	TeamID string
	ClusterID string
	Namespace string
	Workload string
	ObjectUID string
	ActionID string
	ActionVersion uint64
	TargetGroupID string
}
func (s ScopeRef) Validate() error {
	if !validID(s.OrganizationID) { return invalid("ScopeRef.OrganizationID") }
	switch s.Kind {
	case ScopeOrganization:
	case ScopeCluster: if !validID(s.ClusterID) { return invalid("ScopeRef.ClusterID") }
	case ScopeNamespace: if !validID(s.ClusterID) || s.Namespace == "" { return invalid("ScopeRef.Namespace") }
	case ScopeWorkload: if !validID(s.ClusterID) || s.Namespace == "" || s.Workload == "" { return invalid("ScopeRef.Workload") }
	case ScopeObject: if !validID(s.ClusterID) || !validID(s.ObjectUID) { return invalid("ScopeRef.ObjectUID") }
	case ScopeAction: if !validID(s.ClusterID) || !validID(s.ObjectUID) || !validID(s.ActionID) || s.ActionVersion == 0 { return invalid("ScopeRef.Action") }
	case ScopeTargetGroup: if !validID(s.TargetGroupID) { return invalid("ScopeRef.TargetGroupID") }
	default: return invalid("ScopeRef.Kind")
	}
	if s.TeamID != "" && !validID(s.TeamID) { return invalid("ScopeRef.TeamID") }
	return nil
}

type ActionRef struct { ID string; Version uint64 }
func (a ActionRef) Validate() error {
	if !validID(a.ID) || a.Version == 0 { return invalid("ActionRef") }
	return nil
}
func (a ActionRef) Equal(b ActionRef) bool { return a.ID == b.ID && a.Version == b.Version }

type TimeWindow struct { StartMinute int; EndMinute int; Days []time.Weekday }
func (w TimeWindow) Validate() error {
	if w.StartMinute < 0 || w.StartMinute >= 1440 || w.EndMinute < 0 || w.EndMinute >= 1440 || w.StartMinute >= w.EndMinute { return invalid("TimeWindow") }
	seen := map[time.Weekday]bool{}
	for _, d := range w.Days { if d < time.Sunday || d > time.Saturday || seen[d] { return invalid("TimeWindow.Days") }; seen[d] = true }
	return nil
}

type Budget struct {
	MaxExecutions uint64
	Period time.Duration
	MaxConcurrent uint32
}
func (b Budget) Validate() error {
	if b.MaxExecutions == 0 && b.MaxConcurrent == 0 { return invalid("Budget") }
	if b.MaxExecutions > 0 && b.Period <= 0 { return invalid("Budget.Period") }
	if b.Period < 0 { return invalid("Budget.Period") }
	return nil
}

// Constraints is the single declarative schema shared by Policy and Profile.
// Nil AllowedActions means no additional allow-list; a non-nil list is restrictive.
type Constraints struct {
	ModeCeiling Mode
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
func (c Constraints) Validate() error {
	switch c.ModeCeiling { case Observe, Assisted, Autonomous: default: return invalid("Constraints.ModeCeiling") }
	if c.MaxRisk > 100 { return invalid("Constraints.MaxRisk") }
	for _, a := range c.AllowedActions { if err := a.Validate(); err != nil { return err } }
	for _, a := range c.DeniedActions { if err := a.Validate(); err != nil { return err } }
	for _, a := range c.RequiredApprovals { if !validID(a) { return invalid("Constraints.RequiredApprovals") } }
	for _, b := range c.Budgets { if err := b.Validate(); err != nil { return err } }
	for _, w := range c.AllowedTimeWindows { if err := w.Validate(); err != nil { return err } }
	return nil
}

type Lifecycle string
const (
	Active Lifecycle = "active"
	Deactivated Lifecycle = "deactivated"
)

type Provenance struct {
	CreatedByKind string
	CreatedByID string
	UpdatedByKind string
	UpdatedByID string
	CreatedAt time.Time
	UpdatedAt time.Time
}
func (p Provenance) Validate() error {
	if !validID(p.CreatedByKind) || !validID(p.CreatedByID) || !validID(p.UpdatedByKind) || !validID(p.UpdatedByID) || p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) { return invalid("Provenance") }
	return nil
}

type Policy struct {
	ID string
	Owner OwnerScope
	Scope ScopeRef
	Constraints Constraints
	Revision uint64
	Lifecycle Lifecycle
	Provenance Provenance
}
type Profile struct {
	ID string
	Owner OwnerScope
	Constraints Constraints
	Revision uint64
	Lifecycle Lifecycle
	Provenance Provenance
}
type Binding struct {
	ID string
	ProfileID string
	Owner OwnerScope
	Scope ScopeRef
	Revision uint64
	Lifecycle Lifecycle
	Provenance Provenance
}

func (p Policy) Validate() error {
	if !validID(p.ID) || p.Revision == 0 { return invalid("Policy") }
	if err := p.Owner.Validate(); err != nil { return err }; if err := p.Scope.Validate(); err != nil { return err }; if !scopeWithinOwner(p.Scope,p.Owner) { return invalid("Policy.Owner/Scope") }
	if err := p.Constraints.Validate(); err != nil { return err }; if err := p.Provenance.Validate(); err != nil { return err }
	if p.Lifecycle != Active && p.Lifecycle != Deactivated { return invalid("Policy.Lifecycle") }; return nil
}
func (p Profile) Validate() error {
	if !validID(p.ID) || p.Revision == 0 { return invalid("Profile") }
	if err := p.Owner.Validate(); err != nil { return err }; if err := p.Constraints.Validate(); err != nil { return err }
	if err := p.Provenance.Validate(); err != nil { return err }
	if p.Lifecycle != Active && p.Lifecycle != Deactivated { return invalid("Profile.Lifecycle") }; return nil
}
func (b Binding) Validate() error {
	if !validID(b.ID) || !validID(b.ProfileID) || b.Revision == 0 { return invalid("Binding") }
	if err := b.Owner.Validate(); err != nil { return err }; if err := b.Scope.Validate(); err != nil { return err }; if !scopeWithinOwner(b.Scope,b.Owner) { return invalid("Binding.Owner/Scope") }
	if err := b.Provenance.Validate(); err != nil { return err }
	if b.Lifecycle != Active && b.Lifecycle != Deactivated { return invalid("Binding.Lifecycle") }; return nil
}

type MutationMeta struct { ExpectedRevision uint64; ActorKind string; ActorID string; At time.Time }
func (m MutationMeta) Validate() error {
	if m.ExpectedRevision == 0 || !validID(m.ActorKind) || !validID(m.ActorID) || m.At.IsZero() { return invalid("MutationMeta") }; return nil
}

// Repository contracts for Lane C. Implementations MUST perform each write atomically using
// ExpectedRevision compare-and-swap; successful semantic updates increment Revision exactly once.
// Deactivate writes a tombstone; physical deletion is forbidden while references exist.
type PolicyRepository interface {
	CreatePolicy(ctx context.Context, value Policy) error
	GetPolicy(ctx context.Context, owner OwnerScope, id string) (Policy, error)
	GetPolicyRevision(ctx context.Context, owner OwnerScope, id string, revision uint64) (Policy, error)
	UpdatePolicy(ctx context.Context, owner OwnerScope, id string, expectedRevision uint64, next Policy) (Policy, error)
	DeactivatePolicy(ctx context.Context, owner OwnerScope, id string, expectedRevision uint64, meta MutationMeta) (Policy, error)
}
type ProfileRepository interface {
	CreateProfile(ctx context.Context, value Profile) error
	GetProfile(ctx context.Context, owner OwnerScope, id string) (Profile, error)
	GetProfileRevision(ctx context.Context, owner OwnerScope, id string, revision uint64) (Profile, error)
	UpdateProfile(ctx context.Context, owner OwnerScope, id string, expectedRevision uint64, next Profile) (Profile, error)
	DeactivateProfile(ctx context.Context, owner OwnerScope, id string, expectedRevision uint64, meta MutationMeta) (Profile, error)
}
type BindingRepository interface {
	CreateBinding(ctx context.Context, value Binding) error
	GetBinding(ctx context.Context, owner OwnerScope, id string) (Binding, error)
	GetBindingRevision(ctx context.Context, owner OwnerScope, id string, revision uint64) (Binding, error)
	UpdateBinding(ctx context.Context, owner OwnerScope, id string, expectedRevision uint64, next Binding) (Binding, error)
	DeactivateBinding(ctx context.Context, owner OwnerScope, id string, expectedRevision uint64, meta MutationMeta) (Binding, error)
}


func scopeWithinOwner(scope ScopeRef, owner OwnerScope) bool {
	if scope.OrganizationID != owner.OrganizationID { return false }
	if owner.Kind == OwnerOrganization { return true }
	return owner.Kind == OwnerTeam && scope.TeamID == owner.TeamID
}

func validID(s string) bool {
	if s == "" || len(s) > 128 { return false }
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') { continue }
		if i > 0 && strings.ContainsRune("._:@+=-", r) { continue }
		return false
	}
	return true
}
func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }
