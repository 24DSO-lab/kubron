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

// incidentBackend — PostgreSQL-реализация incident.Backend (таблицы incidents, incident_fragments,
// incident_settings; миграция 0011). Семантика совпадает с файловой: её проверяют общие контрактные тесты
// incidenttest. Каждая запись и каждый запрос несут область (организация, команда, кластер); транзакций между
// записями нет — составные действия выполняются одним оператором.
type incidentBackend struct{ s *Store }

var _ incident.Backend = incidentBackend{}

// newIncidentBackend возвращает хранилище инцидентов. Метод доступа Store.Incidents() добавляет слой связки.
func newIncidentBackend(s *Store) incidentBackend { return incidentBackend{s} }

// fragmentAAD привязывает шифротекст фрагмента к его строке: перенос шифротекста в другую строку обнаруживается.
func fragmentAAD(s tenant.Scope, incidentID, id string) []byte {
	return []byte("incident/" + s.OrgID + "/" + s.TeamID + "/" + s.ClusterID + "/" + incidentID + "/" + id + "/text")
}

// PutIncident implements incident.Backend.
func (b incidentBackend) PutIncident(ctx context.Context, in *incident.Incident) error {
	if err := incident.CheckScope(in.Scope); err != nil {
		return err
	}
	if !incident.ValidID(in.ID) {
		return incident.ErrInvalidID
	}
	if err := b.s.ready(); err != nil {
		return err
	}
	in.OpenedAt, in.UpdatedAt = incident.Micro(in.OpenedAt), incident.Micro(in.UpdatedAt)
	next := *in
	next.Version = in.Version + 1
	doc, err := json.Marshal(&next)
	if err != nil {
		return err
	}
	summary, err := json.Marshal(next.Summarize())
	if err != nil {
		return err
	}
	var tag int64
	if in.Version == 0 {
		ct, err := b.s.pool.Exec(ctx, `
			INSERT INTO incidents (org_id, team_id, cluster_id, id, status, opened_at, updated_at, version, summary, doc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`,
			in.Scope.OrgID, in.Scope.TeamID, in.Scope.ClusterID, in.ID, string(in.Status), in.OpenedAt, in.UpdatedAt,
			next.Version, summary, doc)
		if err != nil {
			return err
		}
		tag = ct.RowsAffected()
	} else {
		ct, err := b.s.pool.Exec(ctx, `
			UPDATE incidents SET status=$5, opened_at=$6, updated_at=$7, version=$8, summary=$9, doc=$10
			WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4 AND version=$11`,
			in.Scope.OrgID, in.Scope.TeamID, in.Scope.ClusterID, in.ID, string(in.Status), in.OpenedAt, in.UpdatedAt,
			next.Version, summary, doc, in.Version)
		if err != nil {
			return err
		}
		tag = ct.RowsAffected()
	}
	if tag == 0 {
		return incident.ErrConflict
	}
	in.Version = next.Version
	return nil
}

// GetIncident implements incident.Backend.
func (b incidentBackend) GetIncident(ctx context.Context, scope tenant.Scope, id string) (*incident.Incident, error) {
	if err := incident.CheckScope(scope); err != nil {
		return nil, err
	}
	if !incident.ValidID(id) {
		return nil, incident.ErrInvalidID
	}
	var doc []byte
	err := b.s.pool.QueryRow(ctx, `SELECT doc FROM incidents WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4`,
		scope.OrgID, scope.TeamID, scope.ClusterID, id).Scan(&doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, incident.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var in incident.Incident
	if err := json.Unmarshal(doc, &in); err != nil {
		return nil, fmt.Errorf("corrupted incident %s: %w", id, err)
	}
	return &in, nil
}

// ListIncidents implements incident.Backend. Порядок — «время открытия, ID» по убыванию; ID сравниваются побайтно
// (COLLATE "C"), как в файловой реализации.
func (b incidentBackend) ListIncidents(ctx context.Context, scope tenant.Scope, q incident.ListQuery) (incident.Page, error) {
	if err := incident.CheckScope(scope); err != nil {
		return incident.Page{}, err
	}
	curT, curID, hasCur, err := incident.DecodeCursor(q.Cursor)
	if err != nil {
		return incident.Page{}, err
	}
	var after *time.Time
	if hasCur {
		after = &curT
	}
	n := q.PageSize()
	rows, err := b.s.pool.Query(ctx, `
		SELECT summary FROM incidents
		WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3
		  AND ($4 = '' OR status = $4)
		  AND ($5::timestamptz IS NULL OR (opened_at, id COLLATE "C") < ($5::timestamptz, $6::text COLLATE "C"))
		ORDER BY opened_at DESC, id COLLATE "C" DESC LIMIT $7`,
		scope.OrgID, scope.TeamID, scope.ClusterID, string(q.Status), after, curID, n+1)
	if err != nil {
		return incident.Page{}, err
	}
	defer rows.Close()
	page := incident.Page{Items: []incident.Summary{}}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return incident.Page{}, err
		}
		var s incident.Summary
		if err := json.Unmarshal(raw, &s); err != nil {
			return incident.Page{}, fmt.Errorf("corrupted incident summary: %w", err)
		}
		if len(page.Items) == n {
			last := page.Items[n-1]
			page.NextCursor = incident.EncodeCursor(last.OpenedAt, last.ID)
			break
		}
		page.Items = append(page.Items, s)
	}
	return page, rows.Err()
}

// DeleteIncident implements incident.Backend: инцидент и его фрагменты — одним оператором.
func (b incidentBackend) DeleteIncident(ctx context.Context, scope tenant.Scope, id string) error {
	if err := incident.CheckScope(scope); err != nil {
		return err
	}
	if !incident.ValidID(id) {
		return incident.ErrInvalidID
	}
	var n int
	err := b.s.pool.QueryRow(ctx, `
		WITH f AS (DELETE FROM incident_fragments WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND incident_id=$4),
		     d AS (DELETE FROM incidents WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4 RETURNING 1)
		SELECT count(*) FROM d`, scope.OrgID, scope.TeamID, scope.ClusterID, id).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return incident.ErrNotFound
	}
	return nil
}

// PutFragment implements incident.Backend. Текст шифруется текущим ключом связки.
func (b incidentBackend) PutFragment(ctx context.Context, f *incident.Fragment) error {
	if err := incident.CheckScope(f.Scope); err != nil {
		return err
	}
	if !incident.ValidID(f.IncidentID) || !incident.ValidID(f.ID) {
		return incident.ErrInvalidID
	}
	f.CapturedAt, f.CreatedAt = incident.Micro(f.CapturedAt), incident.Micro(f.CreatedAt)
	meta, err := json.Marshal(f.FragmentMeta)
	if err != nil {
		return err
	}
	ct, keyID, err := b.s.keys.Seal([]byte(f.Text), fragmentAAD(f.Scope, f.IncidentID, f.ID))
	if err != nil {
		return err
	}
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO incident_fragments (org_id, team_id, cluster_id, incident_id, id, meta, text_enc, key_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (org_id, team_id, cluster_id, incident_id, id)
		DO UPDATE SET meta=EXCLUDED.meta, text_enc=EXCLUDED.text_enc, key_id=EXCLUDED.key_id, created_at=EXCLUDED.created_at`,
		f.Scope.OrgID, f.Scope.TeamID, f.Scope.ClusterID, f.IncidentID, f.ID, meta, ct, keyID, f.CreatedAt)
	return err
}

// GetFragment implements incident.Backend.
func (b incidentBackend) GetFragment(ctx context.Context, scope tenant.Scope, incidentID, fragmentID string) (*incident.Fragment, error) {
	if err := incident.CheckScope(scope); err != nil {
		return nil, err
	}
	if !incident.ValidID(incidentID) || !incident.ValidID(fragmentID) {
		return nil, incident.ErrInvalidID
	}
	var (
		meta, ct []byte
		keyID    string
	)
	err := b.s.pool.QueryRow(ctx, `
		SELECT meta, text_enc, key_id FROM incident_fragments
		WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND incident_id=$4 AND id=$5`,
		scope.OrgID, scope.TeamID, scope.ClusterID, incidentID, fragmentID).Scan(&meta, &ct, &keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, incident.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var f incident.Fragment
	if err := json.Unmarshal(meta, &f.FragmentMeta); err != nil {
		return nil, fmt.Errorf("corrupted fragment %s: %w", fragmentID, err)
	}
	plain, err := b.s.keys.Open(ct, keyID, fragmentAAD(scope, incidentID, fragmentID))
	if err != nil {
		return nil, fmt.Errorf("fragment %s cannot be decrypted: %w", fragmentID, err)
	}
	f.Text = string(plain)
	return &f, nil
}

// ListFragments implements incident.Backend: только метаданные, текст не читается и не расшифровывается.
func (b incidentBackend) ListFragments(ctx context.Context, scope tenant.Scope, incidentID string) ([]incident.FragmentMeta, error) {
	if err := incident.CheckScope(scope); err != nil {
		return nil, err
	}
	if !incident.ValidID(incidentID) {
		return nil, incident.ErrInvalidID
	}
	rows, err := b.s.pool.Query(ctx, `
		SELECT meta FROM incident_fragments WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND incident_id=$4
		ORDER BY created_at, id COLLATE "C"`, scope.OrgID, scope.TeamID, scope.ClusterID, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []incident.FragmentMeta{}
	for rows.Next() {
		var raw []byte
		var m incident.FragmentMeta
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("corrupted fragment metadata: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteFragment implements incident.Backend.
func (b incidentBackend) DeleteFragment(ctx context.Context, scope tenant.Scope, incidentID, fragmentID string) error {
	if err := incident.CheckScope(scope); err != nil {
		return err
	}
	if !incident.ValidID(incidentID) || !incident.ValidID(fragmentID) {
		return incident.ErrInvalidID
	}
	ct, err := b.s.pool.Exec(ctx, `
		DELETE FROM incident_fragments WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND incident_id=$4 AND id=$5`,
		scope.OrgID, scope.TeamID, scope.ClusterID, incidentID, fragmentID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return incident.ErrNotFound
	}
	return nil
}

// Settings implements incident.Backend.
func (b incidentBackend) Settings(ctx context.Context, orgID string) (incident.Settings, bool, error) {
	if !incident.ValidID(orgID) {
		return incident.Settings{}, false, incident.ErrInvalidID
	}
	var raw []byte
	err := b.s.pool.QueryRow(ctx, `SELECT settings FROM incident_settings WHERE org_id=$1`, orgID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return incident.Settings{}, false, nil
	}
	if err != nil {
		return incident.Settings{}, false, err
	}
	var s incident.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return incident.Settings{}, false, fmt.Errorf("corrupted incident settings: %w", err)
	}
	return s.Normalize(), true, nil
}

// PutSettings implements incident.Backend.
func (b incidentBackend) PutSettings(ctx context.Context, orgID string, s incident.Settings) error {
	if !incident.ValidID(orgID) {
		return incident.ErrInvalidID
	}
	s = s.Normalize()
	if err := s.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO incident_settings (org_id, settings, updated_at) VALUES ($1,$2,now())
		ON CONFLICT (org_id) DO UPDATE SET settings=EXCLUDED.settings, updated_at=now()`, orgID, raw)
	return err
}

// OrgIDs implements incident.Backend.
func (b incidentBackend) OrgIDs(ctx context.Context) ([]string, error) {
	rows, err := b.s.pool.Query(ctx, `
		SELECT org_id FROM (SELECT org_id FROM incidents UNION SELECT org_id FROM incident_fragments
		                    UNION SELECT org_id FROM incident_settings) o ORDER BY org_id COLLATE "C"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Purge implements incident.Backend: один оператор удаляет просроченные инциденты вместе с фрагментами и
// просроченные фрагменты; каждый фрагмент считается один раз.
func (b incidentBackend) Purge(ctx context.Context, orgID string, fragmentsBefore, incidentsBefore time.Time) (incident.PurgeResult, error) {
	var res incident.PurgeResult
	if !incident.ValidID(orgID) {
		return res, incident.ErrInvalidID
	}
	err := b.s.pool.QueryRow(ctx, `
		WITH di AS (DELETE FROM incidents WHERE org_id=$1 AND updated_at < $3 RETURNING team_id, cluster_id, id),
		     df AS (DELETE FROM incident_fragments f WHERE f.org_id=$1 AND (f.created_at < $2 OR EXISTS (
		              SELECT 1 FROM di WHERE di.team_id=f.team_id AND di.cluster_id=f.cluster_id AND di.id=f.incident_id))
		            RETURNING 1)
		SELECT (SELECT count(*) FROM di), (SELECT count(*) FROM df)`,
		orgID, fragmentsBefore.UTC(), incidentsBefore.UTC()).Scan(&res.Incidents, &res.Fragments)
	return res, err
}

// RekeyIncidentFragments перешифровывает текущим ключом фрагменты, зашифрованные другим (часть Store.Rekey).
// Запись обновляется, только если её не успели изменить (условие по key_id и шифротексту).
func (s *Store) RekeyIncidentFragments(ctx context.Context) (int, error) {
	cur := s.keys.CurrentID()
	rows, err := s.pool.Query(ctx, `
		SELECT org_id, team_id, cluster_id, incident_id, id, text_enc, key_id FROM incident_fragments WHERE key_id <> $1`, cur)
	if err != nil {
		return 0, err
	}
	type row struct {
		scope                 tenant.Scope
		incidentID, id, keyID string
		ct                    []byte
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.scope.OrgID, &r.scope.TeamID, &r.scope.ClusterID, &r.incidentID, &r.id, &r.ct, &r.keyID); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	changed := 0
	for _, r := range todo {
		aad := fragmentAAD(r.scope, r.incidentID, r.id)
		plain, err := s.keys.Open(r.ct, r.keyID, aad)
		if err != nil {
			return changed, err
		}
		ct, keyID, err := s.keys.Seal(plain, aad)
		if err != nil {
			return changed, err
		}
		tag, err := s.pool.Exec(ctx, `
			UPDATE incident_fragments SET text_enc=$6, key_id=$7
			WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND incident_id=$4 AND id=$5 AND key_id=$8 AND text_enc=$9`,
			r.scope.OrgID, r.scope.TeamID, r.scope.ClusterID, r.incidentID, r.id, ct, keyID, r.keyID, r.ct)
		if err != nil {
			return changed, err
		}
		changed += int(tag.RowsAffected())
	}
	return changed, nil
}

// CheckIncidentKeys проверяет, что связка ключей читает фрагменты в БД: по одной записи на каждый идентификатор
// ключа (часть Store.CheckKey). Пустая таблица — не ошибка.
func (s *Store) CheckIncidentKeys(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (key_id) org_id, team_id, cluster_id, incident_id, id, text_enc, key_id
		FROM incident_fragments ORDER BY key_id, org_id, team_id, cluster_id, incident_id, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			sc                    tenant.Scope
			incidentID, id, keyID string
			ct                    []byte
		)
		if err := rows.Scan(&sc.OrgID, &sc.TeamID, &sc.ClusterID, &incidentID, &id, &ct, &keyID); err != nil {
			return err
		}
		if _, err := s.keys.Open(ct, keyID, fragmentAAD(sc, incidentID, id)); err != nil {
			return fmt.Errorf("incident fragment with key %q cannot be read by the key ring: %w", keyID, err)
		}
	}
	return rows.Err()
}
