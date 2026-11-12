// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Readiness — readiness.Backend поверх таблицы readiness_reports (ADR-0025 §6). Отчёты не содержат данных,
// которым нужно шифрование (ADR-0008), поэтому Rekey их не затрагивает.
func (s *Store) Readiness() readiness.Backend { return readinessBackend{s} }

type readinessBackend struct{ s *Store }

var _ readiness.Backend = readinessBackend{}

func (b readinessBackend) Save(ctx context.Context, scope tenant.Scope, r *readiness.Report) error {
	if err := readiness.ValidateForSave(scope, r); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to encode the readiness report: %w", err)
	}
	sum := r.Summary()
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO readiness_reports (org_id, team_id, cluster_id, id, created_at, initiator, current_version,
		    target_version, blockers, warnings, infos, not_checked, schema_version, report)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (org_id, team_id, cluster_id, id) DO UPDATE SET
		    created_at = EXCLUDED.created_at, initiator = EXCLUDED.initiator,
		    current_version = EXCLUDED.current_version, target_version = EXCLUDED.target_version,
		    blockers = EXCLUDED.blockers, warnings = EXCLUDED.warnings, infos = EXCLUDED.infos,
		    not_checked = EXCLUDED.not_checked, schema_version = EXCLUDED.schema_version, report = EXCLUDED.report`,
		scope.OrgID, scope.TeamID, scope.ClusterID, r.ID, r.CreatedAt, r.Initiator, sum.CurrentVersion,
		sum.TargetVersion, sum.Blockers, sum.Warnings, sum.Infos, sum.NotChecked, r.SchemaVersion, raw)
	if err != nil {
		return fmt.Errorf("failed to save the readiness report: %w", err)
	}
	return nil
}

func (b readinessBackend) Get(ctx context.Context, scope tenant.Scope, id string) (*readiness.Report, error) {
	if err := readiness.ValidateScope(scope); err != nil {
		return nil, err
	}
	if !readiness.ValidID(id) {
		return nil, readiness.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var raw []byte
	err := b.s.pool.QueryRow(ctx, `
		SELECT report FROM readiness_reports
		WHERE org_id = $1 AND team_id = $2 AND cluster_id = $3 AND id = $4`,
		scope.OrgID, scope.TeamID, scope.ClusterID, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, readiness.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the readiness report: %w", err)
	}
	var r readiness.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("failed to decode the readiness report: %w", err)
	}
	return &r, nil
}

func (b readinessBackend) List(ctx context.Context, scope tenant.Scope, q readiness.HistoryQuery) (readiness.Page, error) {
	if err := readiness.ValidateScope(scope); err != nil {
		return readiness.Page{}, err
	}
	cur, hasCur, err := readiness.DecodeCursor(q.Cursor)
	if err != nil {
		return readiness.Page{}, err
	}
	limit := readiness.PageSize(q.Limit)
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	// Курсор — «строго старше (created_at, id)»; без него условие всегда истинно. Лишняя строка — признак следующей страницы.
	rows, err := b.s.pool.Query(ctx, `
		SELECT id, created_at, initiator, current_version, target_version, blockers, warnings, infos, not_checked
		FROM readiness_reports
		WHERE org_id = $1 AND team_id = $2 AND cluster_id = $3
		  AND (NOT $4::boolean OR (created_at, id) < ($5::timestamptz, $6::text))
		ORDER BY created_at DESC, id DESC
		LIMIT $7`,
		scope.OrgID, scope.TeamID, scope.ClusterID, hasCur, cur.CreatedAt, cur.ID, limit+1)
	if err != nil {
		return readiness.Page{}, fmt.Errorf("failed to list readiness reports: %w", err)
	}
	defer rows.Close()
	page := readiness.Page{Items: []readiness.Summary{}}
	for rows.Next() {
		s := readiness.Summary{ClusterID: scope.ClusterID}
		if err := rows.Scan(&s.ID, &s.CreatedAt, &s.Initiator, &s.CurrentVersion, &s.TargetVersion,
			&s.Blockers, &s.Warnings, &s.Infos, &s.NotChecked); err != nil {
			return readiness.Page{}, fmt.Errorf("failed to read a readiness summary: %w", err)
		}
		s.CreatedAt = s.CreatedAt.UTC()
		page.Items = append(page.Items, s)
	}
	if err := rows.Err(); err != nil {
		return readiness.Page{}, fmt.Errorf("failed to list readiness reports: %w", err)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.Next = readiness.EncodeCursor(readiness.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (b readinessBackend) Prune(ctx context.Context, now time.Time, r readiness.Retention) (int, error) {
	if r.KeepPerCluster <= 0 && r.MaxAge <= 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	removed := 0
	if r.MaxAge > 0 {
		tag, err := b.s.pool.Exec(ctx, `DELETE FROM readiness_reports WHERE created_at < $1`, now.Add(-r.MaxAge))
		if err != nil {
			return removed, fmt.Errorf("failed to prune readiness reports: %w", err)
		}
		removed += int(tag.RowsAffected())
	}
	if r.KeepPerCluster > 0 {
		tag, err := b.s.pool.Exec(ctx, `
			DELETE FROM readiness_reports t USING (
			    SELECT org_id, team_id, cluster_id, id FROM (
			        SELECT org_id, team_id, cluster_id, id,
			               row_number() OVER (PARTITION BY org_id, team_id, cluster_id ORDER BY created_at DESC, id DESC) AS rn
			        FROM readiness_reports) ranked
			    WHERE rn > $1) old
			WHERE t.org_id = old.org_id AND t.team_id = old.team_id AND t.cluster_id = old.cluster_id AND t.id = old.id`,
			r.KeepPerCluster)
		if err != nil {
			return removed, fmt.Errorf("failed to prune readiness reports: %w", err)
		}
		removed += int(tag.RowsAffected())
	}
	return removed, nil
}
