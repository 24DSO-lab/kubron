// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package autonomy

import (
	"fmt"
	"time"
)

// ApplyPolicyUpdate applies a validated CAS update in memory. Persistence adapters must commit the
// returned aggregate with the same ExpectedRevision predicate in one atomic operation.
func ApplyPolicyUpdate(current, next Policy, meta MutationMeta) (Policy, error) {
	if err := meta.Validate(); err != nil { return Policy{}, err }
	if err := current.Validate(); err != nil { return Policy{}, err }
	if current.Revision != meta.ExpectedRevision { return Policy{}, ErrConflict }
	if current.Lifecycle != Active { return Policy{}, ErrNotFound }
	if next.ID != current.ID || next.Owner != current.Owner { return Policy{}, invalid("Policy immutable identity/owner") }
	next.Revision = current.Revision + 1
	next.Provenance = updatedProvenance(current.Provenance, meta)
	if err := next.Validate(); err != nil { return Policy{}, err }
	return next, nil
}
func ApplyProfileUpdate(current, next Profile, meta MutationMeta) (Profile, error) {
	if err := meta.Validate(); err != nil { return Profile{}, err }
	if err := current.Validate(); err != nil { return Profile{}, err }
	if current.Revision != meta.ExpectedRevision { return Profile{}, ErrConflict }
	if current.Lifecycle != Active { return Profile{}, ErrNotFound }
	if next.ID != current.ID || next.Owner != current.Owner { return Profile{}, invalid("Profile immutable identity/owner") }
	next.Revision = current.Revision + 1
	next.Provenance = updatedProvenance(current.Provenance, meta)
	if err := next.Validate(); err != nil { return Profile{}, err }
	return next, nil
}
func ApplyBindingUpdate(current, next Binding, meta MutationMeta) (Binding, error) {
	if err := meta.Validate(); err != nil { return Binding{}, err }
	if err := current.Validate(); err != nil { return Binding{}, err }
	if current.Revision != meta.ExpectedRevision { return Binding{}, ErrConflict }
	if current.Lifecycle != Active { return Binding{}, ErrNotFound }
	if next.ID != current.ID || next.Owner != current.Owner { return Binding{}, invalid("Binding immutable identity/owner") }
	next.Revision = current.Revision + 1
	next.Provenance = updatedProvenance(current.Provenance, meta)
	if err := next.Validate(); err != nil { return Binding{}, err }
	return next, nil
}
func DeactivatePolicy(current Policy, meta MutationMeta) (Policy, error) {
	if err:=meta.Validate();err!=nil{return Policy{},err}; if err:=current.Validate();err!=nil{return Policy{},err}
	if current.Revision!=meta.ExpectedRevision || current.Revision==^uint64(0){return Policy{},ErrConflict};if current.Lifecycle!=Active{return Policy{},ErrNotFound}
	current.Lifecycle=Deactivated;current.Revision++;current.Provenance=updatedProvenance(current.Provenance,meta);return current,nil
}
func DeactivateProfile(current Profile, meta MutationMeta) (Profile, error) {
	if err:=meta.Validate();err!=nil{return Profile{},err}; if err:=current.Validate();err!=nil{return Profile{},err}
	if current.Revision!=meta.ExpectedRevision || current.Revision==^uint64(0){return Profile{},ErrConflict};if current.Lifecycle!=Active{return Profile{},ErrNotFound}
	current.Lifecycle=Deactivated;current.Revision++;current.Provenance=updatedProvenance(current.Provenance,meta);return current,nil
}
func DeactivateBinding(current Binding, meta MutationMeta) (Binding, error) {
	if err:=meta.Validate();err!=nil{return Binding{},err}; if err:=current.Validate();err!=nil{return Binding{},err}
	if current.Revision!=meta.ExpectedRevision || current.Revision==^uint64(0){return Binding{},ErrConflict};if current.Lifecycle!=Active{return Binding{},ErrNotFound}
	current.Lifecycle=Deactivated;current.Revision++;current.Provenance=updatedProvenance(current.Provenance,meta);return current,nil
}
func updatedProvenance(p Provenance,m MutationMeta) Provenance {
	p.UpdatedByKind=m.ActorKind;p.UpdatedByID=m.ActorID;p.UpdatedAt=m.At.UTC().Round(0);return p
}

// ValidateBindingProfile prevents owner changes and cross-owner Profile sharing before persistence.
func ValidateBindingProfile(binding Binding, profile Profile) error {
	if err:=binding.Validate();err!=nil{return err};if err:=profile.Validate();err!=nil{return err}
	if binding.ProfileID!=profile.ID || binding.Owner!=profile.Owner { return fmt.Errorf("%w: profile binding owner mismatch",ErrForbidden) }
	if binding.Scope.OrganizationID!=profile.Owner.OrganizationID { return ErrForbidden }
	if profile.Owner.Kind==OwnerTeam && binding.Scope.TeamID!=profile.Owner.TeamID { return ErrForbidden }
	if profile.Owner.Kind==OwnerOrganization && profile.Owner.TeamID!="" { return ErrForbidden }
	return nil
}

// NewProvenance creates canonical provenance for a newly created aggregate.
func NewProvenance(actorKind,actorID string,at time.Time)(Provenance,error){
	p:=Provenance{CreatedByKind:actorKind,CreatedByID:actorID,UpdatedByKind:actorKind,UpdatedByID:actorID,CreatedAt:at.UTC().Round(0),UpdatedAt:at.UTC().Round(0)}
	return p,p.Validate()
}
