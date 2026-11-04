// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Challenges is the PostgreSQL implementation of credential.ChallengeStore (table credential_challenges).
type Challenges struct{ s *Store }

var _ credential.ChallengeStore = (*Challenges)(nil)
var _ credential.AttemptLimiter = (*Challenges)(nil)

// Challenges returns the store of pending sign-in requests.
func (s *Store) Challenges() *Challenges { return &Challenges{s: s} }

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

const chColumns = `id, cluster_id, owner_kind, owner_id, session_id, status, attempts, locked_until, expires_at, created_at, version, state_key_id, state_enc`

func scanChallenge(row pgx.Row) (credential.ChallengeRecord, error) {
	var (
		r      credential.ChallengeRecord
		scope  string
		locked *time.Time
	)
	err := row.Scan(&r.ID, &r.ClusterID, &scope, &r.OwnerID, &r.SessionID, &r.Status, &r.Attempts, &locked, &r.ExpiresAt,
		&r.CreatedAt, &r.Version, &r.KeyID, &r.State)
	if err != nil {
		return credential.ChallengeRecord{}, err
	}
	r.Scope = credential.Scope(scope)
	if locked != nil {
		r.LockedUntil = *locked
	}
	return r, nil
}

// Create implements credential.ChallengeStore.
func (c *Challenges) Create(ctx context.Context, r credential.ChallengeRecord) error {
	tag, err := c.s.pool.Exec(ctx, `INSERT INTO credential_challenges (`+chColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12) ON CONFLICT (id) DO NOTHING`,
		r.ID, r.ClusterID, string(r.Scope), r.OwnerID, r.SessionID, r.Status, r.Attempts, nullTime(r.LockedUntil),
		r.ExpiresAt.UTC(), r.CreatedAt.UTC(), r.KeyID, r.State)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" { // unknown cluster
		return credential.ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return credential.ErrVersionConflict
	}
	return nil
}

// Get implements credential.ChallengeStore.
func (c *Challenges) Get(ctx context.Context, id string) (credential.ChallengeRecord, error) {
	r, err := scanChallenge(c.s.pool.QueryRow(ctx, `SELECT `+chColumns+` FROM credential_challenges WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return credential.ChallengeRecord{}, credential.ErrChallengeNotFound
	}
	return r, err
}

// Swap implements credential.ChallengeStore: one atomic UPDATE guarded by the version.
func (c *Challenges) Swap(ctx context.Context, r credential.ChallengeRecord, expected int64) (credential.ChallengeRecord, error) {
	out, err := scanChallenge(c.s.pool.QueryRow(ctx, `UPDATE credential_challenges SET status=$3, attempts=$4, locked_until=$5,
		expires_at=$6, state_key_id=$7, state_enc=$8, version=version+1
		WHERE id=$1 AND version=$2 RETURNING `+chColumns,
		r.ID, expected, r.Status, r.Attempts, nullTime(r.LockedUntil), r.ExpiresAt.UTC(), r.KeyID, r.State))
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return credential.ChallengeRecord{}, err
	}
	var exists bool
	if qerr := c.s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credential_challenges WHERE id=$1)`, r.ID).Scan(&exists); qerr != nil {
		return credential.ChallengeRecord{}, qerr
	}
	if !exists {
		return credential.ChallengeRecord{}, credential.ErrChallengeNotFound
	}
	return credential.ChallengeRecord{}, credential.ErrVersionConflict
}

// Delete implements credential.ChallengeStore.
func (c *Challenges) Delete(ctx context.Context, id string) error {
	_, err := c.s.pool.Exec(ctx, `DELETE FROM credential_challenges WHERE id=$1`, id)
	return err
}

// DeleteBy implements credential.ChallengeStore.
func (c *Challenges) DeleteBy(ctx context.Context, f credential.RevokeFilter) (int, error) {
	tag, err := c.s.pool.Exec(ctx, `DELETE FROM credential_challenges
		WHERE ($1 = '' OR cluster_id = $1) AND ($2 = '' OR owner_id = $2) AND ($3 = '' OR owner_kind = $3)`,
		f.ClusterID, f.OwnerID, string(f.Scope))
	return int(tag.RowsAffected()), err
}

// DeleteExpired implements credential.ChallengeStore.
func (c *Challenges) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	tag, err := c.s.pool.Exec(ctx, `DELETE FROM credential_challenges WHERE expires_at < $1`, before.UTC())
	return int(tag.RowsAffected()), err
}


// Check implements credential.AttemptLimiter. Only active lockouts are returned.
func (c *Challenges) Check(ctx context.Context, userKey, idpKey string, now time.Time) (time.Duration, error) {
	var locked *time.Time
	err := c.s.pool.QueryRow(ctx, `SELECT max(locked_until) FROM credential_attempt_limits
		WHERE (dimension='user' AND key_hash=$1)
		   OR ($2 <> '' AND dimension='idp' AND key_hash=$2)`, userKey, idpKey).Scan(&locked)
	if err != nil {
		return 0, err
	}
	if locked != nil && now.Before(*locked) {
		return locked.Sub(now), nil
	}
	return 0, nil
}

// Fail atomically increments the user and IdP counters. Keys are SHA-256 digests generated by the broker.
func (c *Challenges) Fail(ctx context.Context, userKey, idpKey string, now time.Time, window time.Duration, userLimit, idpLimit int, lockout time.Duration) (time.Duration, error) {
	tx, err := c.s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	increment := func(dimension, key string, limit int) (time.Duration, error) {
		if key == "" || limit <= 0 {
			return 0, nil
		}
		var locked *time.Time
		err := tx.QueryRow(ctx, `INSERT INTO credential_attempt_limits AS current
			(dimension, key_hash, failures, window_started, locked_until, updated_at)
			VALUES ($1,$2,1,$3,CASE WHEN $4 <= 1 THEN $3 + $6::interval ELSE NULL END,$3)
			ON CONFLICT (dimension, key_hash) DO UPDATE SET
				failures = CASE
					WHEN (current.locked_until IS NOT NULL AND current.locked_until <= $3)
					  OR current.window_started <= $3 - $5::interval THEN 1
					ELSE current.failures + 1 END,
				window_started = CASE
					WHEN (current.locked_until IS NOT NULL AND current.locked_until <= $3)
					  OR current.window_started <= $3 - $5::interval THEN $3
					ELSE current.window_started END,
				locked_until = CASE
					WHEN current.locked_until > $3 THEN current.locked_until
					WHEN (CASE
						WHEN (current.locked_until IS NOT NULL AND current.locked_until <= $3)
						  OR current.window_started <= $3 - $5::interval THEN 1
						ELSE current.failures + 1 END) >= $4 THEN $3 + $6::interval
					ELSE NULL END,
				updated_at=$3
			RETURNING locked_until`, dimension, key, now.UTC(), limit, window.String(), lockout.String()).Scan(&locked)
		if err != nil {
			return 0, err
		}
		if locked != nil && now.Before(*locked) {
			return locked.Sub(now), nil
		}
		return 0, nil
	}
	wait, err := increment("user", userKey, userLimit)
	if err != nil {
		return 0, err
	}
	if idpKey != "" {
		idpWait, err := increment("idp", idpKey, idpLimit)
		if err != nil {
			return 0, err
		}
		if idpWait > wait {
			wait = idpWait
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return wait, nil
}

// Reset clears both counters after a successful sign-in.
func (c *Challenges) Reset(ctx context.Context, userKey, idpKey string) error {
	_, err := c.s.pool.Exec(ctx, `DELETE FROM credential_attempt_limits
		WHERE (dimension='user' AND key_hash=$1)
		   OR ($2 <> '' AND dimension='idp' AND key_hash=$2)`, userKey, idpKey)
	return err
}

// Sweep removes old counters. The caller retains entries for at least the full lockout/window period.
func (c *Challenges) Sweep(ctx context.Context, before time.Time) (int, error) {
	tag, err := c.s.pool.Exec(ctx, `DELETE FROM credential_attempt_limits WHERE updated_at < $1`, before.UTC())
	return int(tag.RowsAffected()), err
}
