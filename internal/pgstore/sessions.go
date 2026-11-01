// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/24DSO-labs/kubron/internal/websession"
)

// Sessions — общее хранилище веб-сессий (ADR-0016, раздел 1). Реализует websession.Backend.
type Sessions struct{ s *Store }

var _ websession.Backend = (*Sessions)(nil)

// Sessions возвращает хранилище веб-сессий.
func (s *Store) Sessions() *Sessions { return &Sessions{s} }

func (w *Sessions) Create(ctx context.Context, r websession.Record) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err := w.s.pool.Exec(ctx, `
		INSERT INTO web_sessions (token_hash, id, user_id, csrf, created_at, expires_at, last_active_at, source, active_cluster_id, replica_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9::text, ''), $10)`,
		r.TokenHash[:], r.ID, r.UserID, r.CSRF, r.CreatedAt, r.ExpiresAt, r.LastActive, r.Source, r.ActiveCluster, r.ReplicaID)
	return err
}

// Get: сессия действует, если не истёк срок и пользователь существует и не отключён (отключение
// пользователя сразу обрывает его сессии на любой реплике, даже если она ещё не получила событие).
func (w *Sessions) Get(ctx context.Context, h websession.Hash) (websession.Record, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var r websession.Record
	var hash []byte
	err := w.s.pool.QueryRow(ctx, `
		SELECT s.token_hash, s.id, s.user_id, s.csrf, s.created_at, s.expires_at, s.last_active_at, s.source,
		       COALESCE(s.active_cluster_id, ''), s.replica_id
		FROM web_sessions s JOIN users u ON u.id = s.user_id
		WHERE (s.token_hash = $1 OR (s.prev_token_hash = $1 AND s.prev_valid_until > now()))
		  AND s.expires_at > now() AND NOT u.disabled`, h[:]).
		Scan(&hash, &r.ID, &r.UserID, &r.CSRF, &r.CreatedAt, &r.ExpiresAt, &r.LastActive, &r.Source, &r.ActiveCluster, &r.ReplicaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return websession.Record{}, websession.ErrNotFound
	}
	if err != nil {
		return websession.Record{}, err
	}
	copy(r.TokenHash[:], hash)
	return r, nil
}

func (w *Sessions) Touch(ctx context.Context, id string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err := w.s.pool.Exec(ctx, `UPDATE web_sessions SET last_active_at = GREATEST(last_active_at, $2) WHERE id = $1`, id, at)
	return err
}

func (w *Sessions) SetActiveCluster(ctx context.Context, id, clusterID string) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	// Внешний ключ: кластера, которого нет (удалён только что), не запоминаем — это не ошибка.
	_, err := w.s.pool.Exec(ctx, `
		UPDATE web_sessions SET active_cluster_id = (SELECT c.id FROM clusters c WHERE c.id = NULLIF($2::text, ''))
		WHERE id = $1`, id, clusterID)
	return err
}

func (w *Sessions) Rotate(ctx context.Context, id string, newHash websession.Hash, prevValidUntil time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	tag, err := w.s.pool.Exec(ctx, `
		UPDATE web_sessions SET prev_token_hash = token_hash, prev_valid_until = $3, token_hash = $2 WHERE id = $1`,
		id, newHash[:], prevValidUntil)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return websession.ErrNotFound
	}
	return nil
}

func (w *Sessions) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err := w.s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE id = $1`, id)
	return err
}

func (w *Sessions) DeleteUser(ctx context.Context, userID, exceptID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	rows, err := w.s.pool.Query(ctx, `DELETE FROM web_sessions WHERE user_id = $1 AND id <> $2 RETURNING id`, userID, exceptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (w *Sessions) DeleteExpired(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	tag, err := w.s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
