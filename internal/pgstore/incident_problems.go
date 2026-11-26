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

	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// problemBackend — PostgreSQL-реализация incident.ProblemBackend (таблица incident_problems, миграция 0014).
// Семантика совпадает с файловой: её проверяют общие контрактные тесты incidenttest.RunProblems. Область входит
// в ключ строки и в каждый запрос; таблицы расследований не затрагиваются.
type problemBackend struct{ s *Store }

var _ incident.ProblemBackend = problemBackend{}

func newProblemBackend(s *Store) problemBackend { return problemBackend{s} }

// IncidentProblems возвращает хранилище проблем расследований (ADR-0033 §6).
func (s *Store) IncidentProblems() incident.ProblemBackend { return newProblemBackend(s) }

// PutProblem implements incident.ProblemBackend.
func (b problemBackend) PutProblem(ctx context.Context, p *incident.Problem) error {
	if err := incident.CheckScope(p.Scope); err != nil {
		return err
	}
	if !incident.ValidID(p.ID) {
		return incident.ErrInvalidID
	}
	if !p.Status.ValidStatus() {
		return incident.ErrInvalidProblem
	}
	if err := b.s.ready(); err != nil {
		return err
	}
	incident.NormalizeProblem(p)
	next := *p
	next.Version = p.Version + 1
	doc, err := json.Marshal(&next)
	if err != nil {
		return err
	}
	var rows int64
	if p.Version == 0 {
		ct, err := b.s.pool.Exec(ctx, `
			INSERT INTO incident_problems (org_id, team_id, cluster_id, id, status, key, updated_at, version, doc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`,
			p.Scope.OrgID, p.Scope.TeamID, p.Scope.ClusterID, p.ID, string(p.Status), p.Key, p.UpdatedAt, next.Version, doc)
		if err != nil {
			return err
		}
		rows = ct.RowsAffected()
	} else {
		ct, err := b.s.pool.Exec(ctx, `
			UPDATE incident_problems SET status=$5, key=$6, updated_at=$7, version=$8, doc=$9
			WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4 AND version=$10`,
			p.Scope.OrgID, p.Scope.TeamID, p.Scope.ClusterID, p.ID, string(p.Status), p.Key, p.UpdatedAt, next.Version, doc, p.Version)
		if err != nil {
			return err
		}
		rows = ct.RowsAffected()
	}
	if rows == 0 {
		return incident.ErrConflict
	}
	p.Version = next.Version
	return nil
}

// FindOrCreateOpenProblem serializes lookup/create across all application replicas with a
// transaction-scoped advisory lock derived from the complete tenant scope and correlation key.
func (b problemBackend) FindOrCreateOpenProblem(ctx context.Context, candidate *incident.Problem, oldest time.Time) (*incident.Problem, error) {
	if err := incident.CheckScope(candidate.Scope); err != nil {
		return nil, err
	}
	if !incident.ValidID(candidate.ID) {
		return nil, incident.ErrInvalidID
	}
	if candidate.Status != incident.ProblemOpen || candidate.Key == "" || len(candidate.Members) == 0 || candidate.Version != 0 {
		return nil, incident.ErrInvalidProblem
	}
	if err := b.s.ready(); err != nil {
		return nil, err
	}
	incident.NormalizeProblem(candidate)

	sc := candidate.Scope
	lockKey := fmt.Sprintf("%d:%s%d:%s%d:%s%d:%s",
		len(sc.OrgID), sc.OrgID, len(sc.TeamID), sc.TeamID, len(sc.ClusterID), sc.ClusterID, len(candidate.Key), candidate.Key)
	var result *incident.Problem
	err := pgx.BeginFunc(ctx, b.s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(ctx, `
			SELECT doc FROM incident_problems
			WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3
			  AND key=$4 AND status='open' AND updated_at >= $5
			  AND NOT COALESCE(doc->'detached' ? $6, false)
			ORDER BY updated_at DESC, id COLLATE "C" DESC
			LIMIT 1 FOR UPDATE`,
			sc.OrgID, sc.TeamID, sc.ClusterID, candidate.Key, oldest.UTC(), candidate.Members[0].IncidentID).Scan(&raw)
		if err == nil {
			var p incident.Problem
			if err := json.Unmarshal(raw, &p); err != nil {
				return fmt.Errorf("corrupted problem record: %w", err)
			}
			result = &p
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		next := *candidate
		next.Version = 1
		doc, err := json.Marshal(&next)
		if err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `
			INSERT INTO incident_problems (org_id, team_id, cluster_id, id, status, key, updated_at, version, doc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`,
			sc.OrgID, sc.TeamID, sc.ClusterID, next.ID, string(next.Status), next.Key, next.UpdatedAt, next.Version, doc)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return incident.ErrConflict
		}
		result = &next
		return nil
	})
	if err != nil {
		return nil, err
	}
	if candidate.ID == result.ID {
		candidate.Version = result.Version
	}
	return result, nil
}

// GetProblem implements incident.ProblemBackend.
func (b problemBackend) GetProblem(ctx context.Context, scope tenant.Scope, id string) (*incident.Problem, error) {
	if err := incident.CheckScope(scope); err != nil {
		return nil, err
	}
	if !incident.ValidID(id) {
		return nil, incident.ErrInvalidID
	}
	var doc []byte
	err := b.s.pool.QueryRow(ctx, `SELECT doc FROM incident_problems WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4`,
		scope.OrgID, scope.TeamID, scope.ClusterID, id).Scan(&doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, incident.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var p incident.Problem
	if err := json.Unmarshal(doc, &p); err != nil {
		return nil, fmt.Errorf("corrupted problem %s: %w", id, err)
	}
	return &p, nil
}

// ListProblems implements incident.ProblemBackend. Порядок — «время обновления, ID» по убыванию; ID сравниваются
// побайтно (COLLATE "C"), как в файловой реализации.
func (b problemBackend) ListProblems(ctx context.Context, scope tenant.Scope, q incident.ProblemQuery) (incident.ProblemPage, error) {
	if err := incident.CheckScope(scope); err != nil {
		return incident.ProblemPage{}, err
	}
	curT, curID, hasCur, err := incident.DecodeCursor(q.Cursor)
	if err != nil {
		return incident.ProblemPage{}, err
	}
	var after *time.Time
	if hasCur {
		after = &curT
	}
	n := q.PageSize()
	rows, err := b.s.pool.Query(ctx, `
		SELECT doc FROM incident_problems
		WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3
		  AND ($4 = '' OR status = $4)
		  AND ($5::timestamptz IS NULL OR (updated_at, id COLLATE "C") < ($5::timestamptz, $6::text COLLATE "C"))
		ORDER BY updated_at DESC, id COLLATE "C" DESC LIMIT $7`,
		scope.OrgID, scope.TeamID, scope.ClusterID, string(q.Status), after, curID, n+1)
	if err != nil {
		return incident.ProblemPage{}, err
	}
	defer rows.Close()
	page := incident.ProblemPage{Items: []incident.Problem{}}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return incident.ProblemPage{}, err
		}
		var p incident.Problem
		if err := json.Unmarshal(raw, &p); err != nil {
			return incident.ProblemPage{}, fmt.Errorf("corrupted problem record: %w", err)
		}
		if len(page.Items) == n {
			last := page.Items[n-1]
			page.NextCursor = incident.EncodeCursor(last.UpdatedAt, last.ID)
			break
		}
		page.Items = append(page.Items, p)
	}
	return page, rows.Err()
}

// DeleteProblem implements incident.ProblemBackend.
func (b problemBackend) DeleteProblem(ctx context.Context, scope tenant.Scope, id string) error {
	if err := incident.CheckScope(scope); err != nil {
		return err
	}
	if !incident.ValidID(id) {
		return incident.ErrInvalidID
	}
	ct, err := b.s.pool.Exec(ctx, `DELETE FROM incident_problems WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4`,
		scope.OrgID, scope.TeamID, scope.ClusterID, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return incident.ErrNotFound
	}
	return nil
}

// PurgeProblems implements incident.ProblemBackend: один оператор удаляет проблемы организации, обновлённые
// раньше before.
func (b problemBackend) PurgeProblems(ctx context.Context, orgID string, before time.Time) (int, error) {
	if !incident.ValidID(orgID) {
		return 0, incident.ErrInvalidID
	}
	ct, err := b.s.pool.Exec(ctx, `DELETE FROM incident_problems WHERE org_id=$1 AND updated_at < $2`, orgID, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}
