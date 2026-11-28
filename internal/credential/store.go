// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/24DSO-lab/kubron/internal/identity"
)

var (
	// ErrNotFound means that no sign-in data is stored for the key.
	ErrNotFound = errors.New("no saved sign-in data was found")
	// ErrVersionConflict means that the record was changed (or created) by someone else since it was read.
	ErrVersionConflict = errors.New("the saved sign-in data was changed concurrently")
	// ErrInvalidKey means that the cluster, scope or owner identifier is not acceptable.
	ErrInvalidKey = errors.New("invalid sign-in data identifier")
	// ErrSecretTooLarge means that the sign-in data exceeds MaxSecretSize.
	ErrSecretTooLarge = errors.New("the sign-in data is too large")
	// ErrLegacyNotAllowed means that a record claims the legacy encryption format where it is not allowed.
	ErrLegacyNotAllowed = errors.New("the legacy encryption format is not allowed for this record")
	// ErrUnknownEncFormat means that the record uses an encryption format this version does not know.
	ErrUnknownEncFormat = errors.New("the sign-in data uses an unknown encryption format")
	// ErrRekeyIncomplete means that some records could not be re-encrypted; the report lists them.
	ErrRekeyIncomplete = errors.New("some sign-in records could not be re-encrypted")
)

const (
	// AnyVersion makes Put an unconditional upsert.
	AnyVersion int64 = -1

	// EncEnvelopeV1 is the format of every record written by this code (see internal/secrets).
	EncEnvelopeV1 = "envelope-v1"
	// EncLegacyV1 marks records copied from clusters.secret_enc by MigrateLegacyOIDC: secretbox with
	// the AAD LegacyTeamAAD. Allowed for the team scope only; never written by Put.
	EncLegacyV1 = "legacy-v1"
	// MaxSecretSize is the largest plaintext a record may hold.
	MaxSecretSize = 64 << 10
	maxOwnerIDLen = 200
)

var clusterIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// Key identifies a record: one sign-in per cluster and owner (legacy) or per cluster, principal and binding
// (technical). It is an exclusive tagged union, like Subject.
type Key struct {
	ClusterID string
	Scope     Scope
	OwnerID   string // legacy ScopeUser/ScopeTeam only

	Principal identity.PrincipalRef // technical ScopePrincipal only
	BindingID string                // technical ScopePrincipal only
}

// Validate checks a LEGACY key (ScopeUser or ScopeTeam). Identifiers end up in the associated data of the
// encryption, so they are restricted to values that cannot be confused with each other.
//
// Validate deliberately rejects every technical key and every key that mixes legacy and technical fields. Stores that
// predate technical credentials (PostgreSQL until the integration owner adds them) call Validate, so a technical
// key can never be written there with its principal and binding silently dropped: it fails closed with
// ErrInvalidKey. Code that supports technical keys calls ValidateAny.
func (k Key) Validate() error {
	if !clusterIDRe.MatchString(k.ClusterID) || (k.Scope != ScopeUser && k.Scope != ScopeTeam) ||
		k.OwnerID == "" || len(k.OwnerID) > maxOwnerIDLen || k.hasTechnicalFields() {
		return ErrInvalidKey
	}
	return nil
}

// ValidateAny checks a key as the exclusive tagged union of the Phase 1 Contract Freeze: a legacy key (Validate) or
// a technical ScopePrincipal key: a Team or Service principal with all identifiers valid, a binding id, and no
// OwnerID. Partial or mixed combinations are ErrInvalidKey; nothing is inferred or defaulted.
func (k Key) ValidateAny() error {
	if k.Scope != ScopePrincipal {
		return k.Validate()
	}
	if !clusterIDRe.MatchString(k.ClusterID) || k.OwnerID != "" || !identity.ValidID(k.BindingID) ||
		!k.Principal.IsTechnical() || k.Principal.Validate() != nil {
		return ErrInvalidKey
	}
	return nil
}

// IsPrincipal reports that the key is a technical (ScopePrincipal) key.
func (k Key) IsPrincipal() bool { return k.Scope == ScopePrincipal }

func (k Key) hasTechnicalFields() bool {
	return k.Principal != (identity.PrincipalRef{}) || k.BindingID != ""
}

// Meta is everything about a record except the secret.
type Meta struct {
	Key
	Kind      Kind
	Principal string // sub or login of the signed-in identity, for audit (not the technical PrincipalRef: that is Key.Principal)
	ExpiresAt *time.Time
	Version   int64
	UpdatedAt time.Time
	KeyID     string // identifier of the master key that protects the record
	EncFormat string // EncEnvelopeV1 or EncLegacyV1
	// LastAuditEventID is the critical audit event of the last assignment of the credential. Mandatory for every
	// technical record (Token refuses it unless the event's outcome is CriticalSucceeded); legacy records may leave
	// it empty. A routine token refresh keeps it: it is not a new assignment.
	LastAuditEventID string
}

// ValidateAny checks the key (ValidateAny) and, for a technical record, that LastAuditEventID is a valid identifier.
func (m Meta) ValidateAny() error {
	if err := m.Key.ValidateAny(); err != nil {
		return err
	}
	if m.Scope == ScopePrincipal && !identity.ValidID(m.LastAuditEventID) {
		return ErrInvalidKey
	}
	return nil
}

// Record is a stored sign-in. Secret is the plaintext (refresh token, password, ...); the store encrypts it.
type Record struct {
	Meta
	Secret []byte
}

// CredentialStore keeps encrypted sign-in data. Implementations: PostgreSQL (internal/pgstore) and a file
// (internal/credential/filestore). Both are covered by the same contract tests (credtest.StoreContract).
type CredentialStore interface {
	// Get returns the record with the secret decrypted. A record that cannot be decrypted is an error,
	// never an empty record.
	Get(ctx context.Context, k Key) (Record, error)
	// Put writes the record. expected is the version the caller read: 0 means "must not exist",
	// AnyVersion means "overwrite". A mismatch is ErrVersionConflict. The result has the new Version.
	Put(ctx context.Context, r Record, expected int64) (Meta, error)
	// Delete removes the record; a missing record is not an error.
	Delete(ctx context.Context, k Key) error
	// DeleteIfVersion removes a record only if it still has expectedVersion. A mismatch, including a missing
	// record, returns ErrVersionConflict. This prevents a stale refresh failure from deleting a newer rotation.
	DeleteIfVersion(ctx context.Context, k Key, expectedVersion int64) error
	// List returns the records of an owner (any scope) without decrypting them, ordered by cluster id.
	List(ctx context.Context, ownerID string) ([]Meta, error)
	// Revoke deletes all records matching the filter (empty fields match anything) and returns their number.
	Revoke(ctx context.Context, f RevokeFilter) (int, error)
}

// PrincipalStore is a CredentialStore that also holds technical (ScopePrincipal) records. The file store implements it;
// the Broker refuses every technical operation on a store that does not (fail closed), because a store without
// support would drop the principal and the binding of a record.
type PrincipalStore interface {
	CredentialStore
	// ListPrincipal returns the records of one technical principal without decrypting them, ordered by cluster
	// id and binding id. It never returns legacy records, whatever their OwnerID.
	ListPrincipal(ctx context.Context, principal identity.PrincipalRef) ([]Meta, error)
}

// Matches reports whether the key is selected by the filter: every non-empty field must be equal. For a legacy
// store key the technical fields of the filter are empty, so they match nothing technical by accident.
func (f RevokeFilter) Matches(k Key) bool {
	return (f.ClusterID == "" || k.ClusterID == f.ClusterID) &&
		(f.OwnerID == "" || k.OwnerID == f.OwnerID) &&
		(f.Scope == "" || k.Scope == f.Scope) &&
		(f.Principal == (identity.PrincipalRef{}) || k.Principal == f.Principal) &&
		(f.BindingID == "" || k.BindingID == f.BindingID)
}

// IsPrincipal reports that the filter targets technical credentials (any technical field is set).
func (f RevokeFilter) IsPrincipal() bool {
	return f.Scope == ScopePrincipal || f.Principal != (identity.PrincipalRef{}) || f.BindingID != ""
}

// ValidatePrincipal checks that a technical filter is exact: ScopePrincipal, a cluster, a Team or Service principal and
// a binding id, and no OwnerID. Wildcards are not accepted for technical credentials.
func (f RevokeFilter) ValidatePrincipal() error {
	return Key{ClusterID: f.ClusterID, Scope: f.Scope, OwnerID: f.OwnerID, Principal: f.Principal, BindingID: f.BindingID}.ValidateAny()
}

// RekeyReport is the outcome of re-encrypting the stored records with the current master key.
type RekeyReport struct {
	Total   int   // records examined
	Changed int   // records written back (new key or legacy format)
	Failed  []Key // records that could not be read; they were left untouched
	// LegacyRemaining is the number of records still in the legacy format after the run
	// (only unreadable ones can remain).
	LegacyRemaining int
}
