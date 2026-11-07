// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"errors"
	"regexp"
	"time"
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

// Key identifies a record: one sign-in per cluster and owner.
type Key struct {
	ClusterID string
	Scope     Scope
	OwnerID   string
}

// Validate checks the key. Identifiers end up in the associated data of the encryption, so they are
// restricted to values that cannot be confused with each other.
func (k Key) Validate() error {
	if !clusterIDRe.MatchString(k.ClusterID) || (k.Scope != ScopeUser && k.Scope != ScopeTeam) ||
		k.OwnerID == "" || len(k.OwnerID) > maxOwnerIDLen {
		return ErrInvalidKey
	}
	return nil
}

// Meta is everything about a record except the secret.
type Meta struct {
	Key
	Kind      Kind
	Principal string
	ExpiresAt *time.Time
	Version   int64
	UpdatedAt time.Time
	KeyID     string // identifier of the master key that protects the record
	EncFormat string // EncEnvelopeV1 or EncLegacyV1
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

// RekeyReport is the outcome of re-encrypting the stored records with the current master key.
type RekeyReport struct {
	Total   int   // records examined
	Changed int   // records written back (new key or legacy format)
	Failed  []Key // records that could not be read; they were left untouched
	// LegacyRemaining is the number of records still in the legacy format after the run
	// (only unreadable ones can remain).
	LegacyRemaining int
}
