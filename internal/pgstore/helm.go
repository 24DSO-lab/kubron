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
)

// Состояние Helm в сервисном режиме (ADR-0015, миграция 0004): репозитории и OCI-реестры с зашифрованными
// паролями и журнал операций. Всё изолировано по командам: каждый метод принимает команду, а запись другой
// команды неотличима от несуществующей (ErrHelmNotFound).

// helmJournalMax — сколько последних операций хранится на команду.
const helmJournalMax = 200

var (
	ErrHelmNotFound = errors.New("helm record not found")
	ErrHelmExists   = errors.New("a helm repository with this name already exists")
)

// helmAAD привязывает шифротекст пароля к команде и имени: скопированный в другую строку он не расшифруется.
// Поэтому репозиторий нельзя переименовать на месте.
func helmAAD(team, name string) []byte { return []byte("helm/" + team + "/" + name) }

// HelmRepoOptions — настройки репозитория, которые хранятся в колонке options.
type HelmRepoOptions struct {
	InsecureTLS bool     `json:"insecureTLS,omitempty"`
	PlainHTTP   bool     `json:"plainHTTP,omitempty"`
	Charts      []string `json:"charts,omitempty"`
}

// HelmRepo — репозиторий или OCI-реестр. Secret — пароль в открытом виде: он расшифровывается только внутри
// ядра и для запросов к самому репозиторию.
type HelmRepo struct {
	Name      string
	Kind      string // http | oci
	URL       string
	Username  string
	Secret    string
	Options   HelmRepoOptions
	CreatedBy string
	CreatedAt time.Time
}

// Helm — состояние Helm поверх таблиц helm_repositories и helm_operations.
func (s *Store) Helm() *Helm { return &Helm{s} }

// Helm — хранилище состояния Helm.
type Helm struct{ s *Store }

// List возвращает репозитории команды с расшифрованными паролями.
func (h *Helm) List(team string) ([]HelmRepo, error) {
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := h.s.pool.Query(ctx, `
		SELECT name, kind, url, username, options, secret_enc, secret_key_id, COALESCE(created_by, ''), created_at
		FROM helm_repositories WHERE team_id = $1 ORDER BY name`, team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HelmRepo
	for rows.Next() {
		var r HelmRepo
		var opts, ct []byte
		var keyID string
		if err := rows.Scan(&r.Name, &r.Kind, &r.URL, &r.Username, &opts, &ct, &keyID, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(opts, &r.Options); err != nil {
			return nil, fmt.Errorf("helm repository options are corrupted: %w", err)
		}
		if ct != nil {
			plain, err := h.s.keys.Open(ct, keyID, helmAAD(team, r.Name))
			if err != nil {
				return nil, err
			}
			r.Secret = string(plain)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Create добавляет репозиторий; занятое имя в команде — ErrHelmExists.
func (h *Helm) Create(team string, r HelmRepo) error {
	if err := h.s.ready(); err != nil {
		return err
	}
	opts, err := json.Marshal(r.Options)
	if err != nil {
		return err
	}
	var enc []byte
	keyID := ""
	if r.Secret != "" {
		if enc, keyID, err = h.s.keys.Seal([]byte(r.Secret), helmAAD(team, r.Name)); err != nil {
			return err
		}
	}
	ctx, cancel := opCtx()
	defer cancel()
	// created_by — только существующий пользователь: идентификатор вида (например, локальный) в users может не быть.
	_, err = h.s.pool.Exec(ctx, `
		INSERT INTO helm_repositories (team_id, name, kind, url, username, options, secret_enc, secret_key_id, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, (SELECT id FROM users WHERE id = $9), $10)`,
		team, r.Name, r.Kind, r.URL, r.Username, opts, enc, keyID, r.CreatedBy, r.CreatedAt)
	switch {
	case err == nil:
		return nil
	case isUnique(err):
		return ErrHelmExists
	case pgCode(err) == "23503": // команды нет
		return ErrHelmNotFound
	}
	return err
}

// SetCharts заменяет набор чартов OCI-реестра.
func (h *Helm) SetCharts(team, name string, charts []string) error {
	ctx, cancel := opCtx()
	defer cancel()
	tag, err := h.s.pool.Exec(ctx, `
		UPDATE helm_repositories SET options = jsonb_set(options, '{charts}', $3::jsonb, true)
		WHERE team_id = $1 AND name = $2 AND kind = 'oci'`, team, name, mustJSON(charts))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrHelmNotFound
	}
	return nil
}

func mustJSON(v any) []byte {
	if s, ok := v.([]string); ok && s == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return b
}

// Delete удаляет репозиторий и возвращает его прежнее содержимое (без пароля).
func (h *Helm) Delete(team, name string) (*HelmRepo, error) {
	ctx, cancel := opCtx()
	defer cancel()
	var r HelmRepo
	var opts []byte
	err := h.s.pool.QueryRow(ctx, `
		DELETE FROM helm_repositories WHERE team_id = $1 AND name = $2
		RETURNING name, kind, url, username, options`, team, name).Scan(&r.Name, &r.Kind, &r.URL, &r.Username, &opts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrHelmNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(opts, &r.Options)
	return &r, nil
}

// AddOperation дописывает запись в журнал команды (повтор того же id заменяет запись) и оставляет последние
// helmJournalMax. record — запись операции целиком в json.
func (h *Helm) AddOperation(team, clusterID, id string, finished time.Time, record []byte) error {
	ctx, cancel := opCtx()
	defer cancel()
	return pgx.BeginFunc(ctx, h.s.pool, func(tx pgx.Tx) error {
		// кластер мог быть удалён за время операции: тогда cluster_id остаётся пустым
		if _, err := tx.Exec(ctx, `
			INSERT INTO helm_operations (team_id, id, cluster_id, finished_at, record)
			VALUES ($1, $2, (SELECT id FROM clusters WHERE id = $3 AND team_id = $1), $4, $5)
			ON CONFLICT (team_id, id) DO UPDATE SET cluster_id = EXCLUDED.cluster_id, finished_at = EXCLUDED.finished_at, record = EXCLUDED.record`,
			team, id, clusterID, finished, record); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			DELETE FROM helm_operations WHERE team_id = $1 AND id NOT IN (
				SELECT id FROM helm_operations WHERE team_id = $1 ORDER BY finished_at DESC, id DESC LIMIT $2)`, team, helmJournalMax)
		return err
	})
}

// Operations возвращает журнал команды, новые первыми (записи в json).
func (h *Helm) Operations(team string) ([][]byte, error) {
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := h.s.pool.Query(ctx, `SELECT record FROM helm_operations WHERE team_id = $1 ORDER BY finished_at DESC, id DESC`, team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var rec []byte
		if err := rows.Scan(&rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Operation возвращает запись журнала команды; чужая и несуществующая — ErrHelmNotFound.
func (h *Helm) Operation(team, id string) ([]byte, error) {
	ctx, cancel := opCtx()
	defer cancel()
	var rec []byte
	err := h.s.pool.QueryRow(ctx, `SELECT record FROM helm_operations WHERE team_id = $1 AND id = $2`, team, id).Scan(&rec)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrHelmNotFound
	}
	return rec, err
}

// ClearOperations стирает журнал команды.
func (h *Helm) ClearOperations(team string) error {
	ctx, cancel := opCtx()
	defer cancel()
	_, err := h.s.pool.Exec(ctx, `DELETE FROM helm_operations WHERE team_id = $1`, team)
	return err
}

// rekeyHelm перешифровывает пароли репозиториев текущим ключом (см. Store.Rekey).
func (s *Store) rekeyHelm(ctx context.Context, cur string) (int, error) {
	type row struct {
		team, name, keyID string
		ct                []byte
	}
	rows, err := s.pool.Query(ctx, `SELECT team_id, name, secret_enc, secret_key_id FROM helm_repositories WHERE secret_enc IS NOT NULL AND secret_key_id <> $1`, cur)
	if err != nil {
		return 0, err
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.team, &r.name, &r.ct, &r.keyID); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range todo {
		plain, err := s.keys.Open(r.ct, r.keyID, helmAAD(r.team, r.name))
		if err != nil {
			return n, err
		}
		ct, keyID, err := s.keys.Seal(plain, helmAAD(r.team, r.name))
		if err != nil {
			return n, err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE helm_repositories SET secret_enc=$3, secret_key_id=$4 WHERE team_id=$1 AND name=$2 AND secret_key_id=$5 AND secret_enc=$6`,
			r.team, r.name, ct, keyID, r.keyID, r.ct)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}
