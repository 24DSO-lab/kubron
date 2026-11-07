// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

// Credentials is the PostgreSQL implementation of credential.CredentialStore (table cluster_credentials).
// Several replicas may use the same database: every write is an atomic statement guarded by the version.
type Credentials struct {
	s      *Store
	sealer *credential.Sealer
}

var _ credential.CredentialStore = (*Credentials)(nil)

// Credentials returns the sign-in data store; secrets are encrypted with the store's master key ring.
func (s *Store) Credentials() *Credentials {
	env, err := secrets.NewEnvelope(s.keys)
	if err != nil { // the ring is mandatory in Open; this cannot happen
		panic(err)
	}
	return &Credentials{s: s, sealer: credential.NewSealer(env)}
}

const credColumns = `kind, secret_key_id, enc_format, principal, expires_at, version, updated_at`

type credRow struct {
	kind, keyID, format, principal string
	expiresAt                      *time.Time
	version                        int64
	updatedAt                      time.Time
}

func (r credRow) meta(k credential.Key) credential.Meta {
	return credential.Meta{Key: k, Kind: credential.Kind(r.kind), Principal: r.principal, ExpiresAt: r.expiresAt,
		Version: r.version, UpdatedAt: r.updatedAt, KeyID: r.keyID, EncFormat: r.format}
}

// Get implements credential.CredentialStore.
func (c *Credentials) Get(ctx context.Context, k credential.Key) (credential.Record, error) {
	if err := k.Validate(); err != nil {
		return credential.Record{}, err
	}
	var (
		r    credRow
		blob []byte
	)
	err := c.s.pool.QueryRow(ctx, `SELECT secret_enc, `+credColumns+`
		FROM cluster_credentials WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3`,
		k.ClusterID, string(k.Scope), k.OwnerID).
		Scan(&blob, &r.kind, &r.keyID, &r.format, &r.principal, &r.expiresAt, &r.version, &r.updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return credential.Record{}, credential.ErrNotFound
	}
	if err != nil {
		return credential.Record{}, err
	}
	plain, err := c.sealer.Open(k, blob, r.keyID, r.format)
	if err != nil {
		return credential.Record{}, err
	}
	return credential.Record{Meta: r.meta(k), Secret: plain}, nil
}

// Put implements credential.CredentialStore.
func (c *Credentials) Put(ctx context.Context, rec credential.Record, expected int64) (credential.Meta, error) {
	k := rec.Key
	if err := k.Validate(); err != nil {
		return credential.Meta{}, err
	}
	blob, keyID, err := c.sealer.Seal(k, rec.Secret)
	if err != nil {
		return credential.Meta{}, err
	}
	args := []any{k.ClusterID, string(k.Scope), k.OwnerID, string(rec.Kind), blob, keyID, credential.EncEnvelopeV1, rec.Principal, rec.ExpiresAt}
	var q string
	switch {
	case expected == 0:
		q = `INSERT INTO cluster_credentials (cluster_id, owner_kind, owner_id, kind, secret_enc, secret_key_id, enc_format, principal, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (cluster_id, owner_kind, owner_id) DO NOTHING
			RETURNING version, updated_at`
	case expected == credential.AnyVersion:
		q = `INSERT INTO cluster_credentials (cluster_id, owner_kind, owner_id, kind, secret_enc, secret_key_id, enc_format, principal, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (cluster_id, owner_kind, owner_id) DO UPDATE SET
				kind=EXCLUDED.kind, secret_enc=EXCLUDED.secret_enc, secret_key_id=EXCLUDED.secret_key_id,
				enc_format=EXCLUDED.enc_format, principal=EXCLUDED.principal, expires_at=EXCLUDED.expires_at,
				version=cluster_credentials.version+1, updated_at=now()
			RETURNING version, updated_at`
	case expected > 0:
		q = `UPDATE cluster_credentials SET kind=$4, secret_enc=$5, secret_key_id=$6, enc_format=$7, principal=$8,
				expires_at=$9, version=version+1, updated_at=now()
			WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3 AND version=$10
			RETURNING version, updated_at`
		args = append(args, expected)
	default:
		return credential.Meta{}, credential.ErrVersionConflict
	}
	m := credential.Meta{Key: k, Kind: rec.Kind, Principal: rec.Principal, ExpiresAt: rec.ExpiresAt, KeyID: keyID, EncFormat: credential.EncEnvelopeV1}
	err = c.s.pool.QueryRow(ctx, q, args...).Scan(&m.Version, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return credential.Meta{}, credential.ErrVersionConflict
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" { // the cluster does not exist
		return credential.Meta{}, credential.ErrNotFound
	}
	if err != nil {
		return credential.Meta{}, err
	}
	return m, nil
}

// Delete implements credential.CredentialStore.
func (c *Credentials) Delete(ctx context.Context, k credential.Key) error {
	if err := k.Validate(); err != nil {
		return err
	}
	_, err := c.s.pool.Exec(ctx, `DELETE FROM cluster_credentials WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3`,
		k.ClusterID, string(k.Scope), k.OwnerID)
	return err
}

// DeleteIfVersion implements credential.CredentialStore. The version predicate makes invalidation safe
// against a refresh-token rotation committed by another replica.
func (c *Credentials) DeleteIfVersion(ctx context.Context, k credential.Key, expectedVersion int64) error {
	if err := k.Validate(); err != nil {
		return err
	}
	tag, err := c.s.pool.Exec(ctx, `DELETE FROM cluster_credentials
		WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3 AND version=$4`,
		k.ClusterID, string(k.Scope), k.OwnerID, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return credential.ErrVersionConflict
	}
	return nil
}

// List implements credential.CredentialStore.
func (c *Credentials) List(ctx context.Context, ownerID string) ([]credential.Meta, error) {
	rows, err := c.s.pool.Query(ctx, `SELECT cluster_id, owner_kind, `+credColumns+`
		FROM cluster_credentials WHERE owner_id=$1 ORDER BY cluster_id, owner_kind`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []credential.Meta
	for rows.Next() {
		var (
			r     credRow
			k     = credential.Key{OwnerID: ownerID}
			scope string
		)
		if err := rows.Scan(&k.ClusterID, &scope, &r.kind, &r.keyID, &r.format, &r.principal, &r.expiresAt, &r.version, &r.updatedAt); err != nil {
			return nil, err
		}
		k.Scope = credential.Scope(scope)
		out = append(out, r.meta(k))
	}
	return out, rows.Err()
}

// Revoke implements credential.CredentialStore.
func (c *Credentials) Revoke(ctx context.Context, f credential.RevokeFilter) (int, error) {
	tag, err := c.s.pool.Exec(ctx, `DELETE FROM cluster_credentials
		WHERE ($1 = '' OR cluster_id = $1) AND ($2 = '' OR owner_id = $2) AND ($3 = '' OR owner_kind = $3)`,
		f.ClusterID, f.OwnerID, string(f.Scope))
	return int(tag.RowsAffected()), err
}

// LegacyMigration is the outcome of MigrateLegacyOIDC.
type LegacyMigration struct {
	Copied  int // new rows
	Updated int // rows refreshed because clusters.secret_enc changed after an earlier copy
	Skipped int // clusters whose copy is already current or was converted to the envelope format
}

// MigrateLegacyOIDC copies the oidc-password secrets of task 3.7 (clusters.secret_enc) into
// cluster_credentials as team-scope rows with enc_format legacy-v1. The ciphertext is copied byte for byte;
// clusters.secret_enc is not modified. The function is idempotent and may run repeatedly until the cut-over:
// a changed source secret refreshes a row that is still legacy-v1, a row already converted to envelope-v1
// (by Put or Rekey) is never overwritten.
func (s *Store) MigrateLegacyOIDC(ctx context.Context) (LegacyMigration, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM clusters WHERE mode='oidc-password' AND secret_enc IS NOT NULL`).Scan(&total); err != nil {
		return LegacyMigration{}, err
	}
	rows, err := s.pool.Query(ctx, `
		INSERT INTO cluster_credentials AS cc (cluster_id, owner_kind, owner_id, kind, secret_enc, secret_key_id, enc_format)
		SELECT id, 'team', team_id, 'oidc', secret_enc, secret_key_id, 'legacy-v1'
		FROM clusters WHERE mode='oidc-password' AND secret_enc IS NOT NULL
		ON CONFLICT (cluster_id, owner_kind, owner_id) DO UPDATE SET
			secret_enc=EXCLUDED.secret_enc, secret_key_id=EXCLUDED.secret_key_id,
			version=cc.version+1, updated_at=now()
		WHERE cc.enc_format='legacy-v1'
		  AND (cc.secret_enc IS DISTINCT FROM EXCLUDED.secret_enc OR cc.secret_key_id IS DISTINCT FROM EXCLUDED.secret_key_id)
		RETURNING (xmax = 0)`)
	if err != nil {
		return LegacyMigration{}, err
	}
	defer rows.Close()
	var res LegacyMigration
	for rows.Next() {
		var inserted bool
		if err := rows.Scan(&inserted); err != nil {
			return LegacyMigration{}, err
		}
		if inserted {
			res.Copied++
		} else {
			res.Updated++
		}
	}
	if err := rows.Err(); err != nil {
		return LegacyMigration{}, err
	}
	res.Skipped = total - res.Copied - res.Updated
	return res, nil
}

// CountLegacyCredentials returns the number of records still in the legacy-v1 format. The server logs a
// warning at start while it is not zero.
func (s *Store) CountLegacyCredentials(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM cluster_credentials WHERE enc_format='legacy-v1'`).Scan(&n)
	return n, err
}

// RekeyCredentials re-encrypts all sign-in records with the current master key and moves legacy-v1 records
// to envelope-v1. A record is rewritten only if nobody changed it meanwhile (condition on the ciphertext),
// so it is safe while the service runs. Unreadable records are left untouched and listed in the report.
func (c *Credentials) RekeyCredentials(ctx context.Context) (credential.RekeyReport, error) {
	var rep credential.RekeyReport
	cur := c.s.keys.CurrentID()
	type row struct {
		k             credential.Key
		blob          []byte
		keyID, format string
	}
	rows, err := c.s.pool.Query(ctx, `SELECT cluster_id, owner_kind, owner_id, secret_enc, secret_key_id, enc_format FROM cluster_credentials`)
	if err != nil {
		return rep, err
	}
	var all []row
	for rows.Next() {
		var r row
		var scope string
		if err := rows.Scan(&r.k.ClusterID, &scope, &r.k.OwnerID, &r.blob, &r.keyID, &r.format); err != nil {
			rows.Close()
			return rep, err
		}
		r.k.Scope = credential.Scope(scope)
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	rep.Total = len(all)
	for _, r := range all {
		if r.format == credential.EncEnvelopeV1 && r.keyID == cur {
			continue // already current; nothing to do (readability is checked by Get, not by a bulk job)
		}
		out, id, changed, err := c.sealer.Rewrap(r.k, r.blob, r.keyID, r.format)
		if err != nil {
			rep.Failed = append(rep.Failed, r.k)
			if r.format == credential.EncLegacyV1 {
				rep.LegacyRemaining++
			}
			continue
		}
		if !changed {
			continue
		}
		tag, err := c.s.pool.Exec(ctx, `UPDATE cluster_credentials SET secret_enc=$4, secret_key_id=$5, enc_format='envelope-v1'
			WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3 AND secret_enc=$6`,
			r.k.ClusterID, string(r.k.Scope), r.k.OwnerID, out, id, r.blob)
		if err != nil {
			return rep, err
		}
		rep.Changed += int(tag.RowsAffected())
	}
	if len(rep.Failed) > 0 {
		return rep, credential.ErrRekeyIncomplete
	}
	return rep, nil
}

// dropOwnerCredentials removes the saved sign-in data and the pending sign-in requests of one owner
// (a user or a team). cluster_credentials.owner_id has no foreign key because it points at either table,
// so deleting the owner has to clean up explicitly, in the same transaction.
func dropOwnerCredentials(ctx context.Context, tx pgx.Tx, ownerKind, ownerID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM cluster_credentials WHERE owner_kind = $1 AND owner_id = $2`, ownerKind, ownerID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM credential_challenges WHERE owner_kind = $1 AND owner_id = $2`, ownerKind, ownerID)
	return err
}

// dropRemovedUsersCredentials does the same for every user of the organization that is not in keep (the
// users that a full directory save is about to delete).
func dropRemovedUsersCredentials(ctx context.Context, tx pgx.Tx, orgID string, keep []string) error {
	const gone = `owner_kind = 'user' AND owner_id IN (SELECT id FROM users WHERE org_id = $1 AND id <> ALL($2))`
	if _, err := tx.Exec(ctx, `DELETE FROM cluster_credentials WHERE `+gone, orgID, keep); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM credential_challenges WHERE `+gone, orgID, keep)
	return err
}
