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

	"github.com/24DSO-labs/kubron/pkg/ext"
)

const settingsKeySecurity = "security"

type securityJSON struct {
	IdleTimeoutMinutes *int `json:"idleTimeoutMinutes,omitempty"`
}

// Users — ext.UserBackend поверх таблиц users и settings. Save заменяет набор пользователей организации целиком
// (как файловое хранилище), поэтому сериализуется блокировкой БД.
func (s *Store) Users() ext.UserBackend { return userBackend{s} }

// UserDelta — точечные операции над пользователями для сервиса с несколькими репликами (см. UpsertUser).
type UserDelta interface {
	UpsertUser(u ext.UserRecord) error
	TouchLogin(id string, at time.Time, passwordHash string) error
	DeleteUser(id string) error
	SaveSettings(ext.SecuritySettings) error
}

// UserDelta возвращает точечные операции над пользователями.
func (s *Store) UserDelta() UserDelta { return userBackend{s} }

type userBackend struct{ s *Store }

func (b userBackend) Load() (*ext.UserData, error) {
	if err := b.s.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := opCtx()
	defer cancel()
	out := &ext.UserData{}
	rows, err := b.s.pool.Query(ctx, `
		SELECT id, username, role, password_hash, disabled, must_change_password, created_at, updated_at, last_login_at
		FROM users WHERE org_id = $1 ORDER BY created_at, id`, b.s.orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u ext.UserRecord
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.PasswordHash, &u.Disabled, &u.MustChangePassword,
			&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		out.Users = append(out.Users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	var raw []byte
	err = b.s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingsKeySecurity).Scan(&raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		var sj securityJSON
		if err := json.Unmarshal(raw, &sj); err != nil {
			return nil, fmt.Errorf("security settings are corrupted: %w", err)
		}
		out.Settings.IdleTimeoutMinutes = sj.IdleTimeoutMinutes
	}
	return out, nil
}

func (b userBackend) Save(d *ext.UserData) error {
	if err := b.s.ready(); err != nil {
		return err
	}
	ids := make([]string, 0, len(d.Users))
	for _, u := range d.Users {
		switch u.Role {
		case "admin", "operator", "user":
		default:
			return fmt.Errorf("unknown role %q", u.Role)
		}
		ids = append(ids, u.ID)
	}
	settings, err := json.Marshal(securityJSON{IdleTimeoutMinutes: d.Settings.IdleTimeoutMinutes})
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	err = pgx.BeginFunc(ctx, b.s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x6B756274)); err != nil {
			return err
		}
		prev := map[string]string{} // прежняя глобальная роль: по ней видно, кого создали или повысили/понизили
		rows, err := tx.Query(ctx, `SELECT id, role FROM users WHERE org_id = $1`, b.s.orgID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, role string
			if err := rows.Scan(&id, &role); err != nil {
				rows.Close()
				return err
			}
			prev[id] = role
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// Сначала удаляем: освобождаются имена, которые могут занять новые записи.
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE org_id = $1 AND id <> ALL($2)`, b.s.orgID, ids); err != nil {
			return err
		}
		now := time.Now()
		for _, u := range d.Users {
			if err := upsertUser(ctx, tx, b.s.orgID, u, now); err != nil {
				return err
			}
		}
		// Переходное правило до API управления командами (3.3): новый пользователь и тот, кому сменили
		// глобальную роль, получает ту же роль в команде «Default». Остальные членства Save не трогает.
		for _, u := range d.Users {
			if old, ok := prev[u.ID]; ok && old == u.Role {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO memberships (user_id, team_id, role) VALUES ($1, $2, $3)
				ON CONFLICT (user_id, team_id) DO UPDATE SET role = EXCLUDED.role`, u.ID, b.s.teamID, u.Role); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, settingsKeySecurity, settings)
		return err
	})
	b.s.invalidateAccess()
	return err
}

// upsertUser записывает одного пользователя (создание или обновление). Конфликт имени — ErrConflict.
func upsertUser(ctx context.Context, tx pgx.Tx, orgID string, u ext.UserRecord, now time.Time) error {
	created, updated := u.CreatedAt, u.UpdatedAt
	if created.IsZero() {
		created = now
	}
	if updated.IsZero() {
		updated = created
	}
	orgRole := "member"
	if u.Role == "admin" {
		orgRole = "org-admin"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, org_id, username, password_hash, org_role, role, disabled, must_change_password, created_at, updated_at, last_login_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, password_hash = EXCLUDED.password_hash,
			org_role = EXCLUDED.org_role, role = EXCLUDED.role, disabled = EXCLUDED.disabled,
			must_change_password = EXCLUDED.must_change_password, updated_at = EXCLUDED.updated_at,
			last_login_at = EXCLUDED.last_login_at
		WHERE users.org_id = EXCLUDED.org_id`,
		u.ID, orgID, u.Username, u.PasswordHash, orgRole, u.Role, u.Disabled, u.MustChangePassword,
		created, updated, u.LastLoginAt); err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: user %q", ErrConflict, u.Username)
		}
		return err
	}
	return nil
}

func validRole(role string) error {
	switch role {
	case "admin", "operator", "user":
		return nil
	}
	return fmt.Errorf("unknown role %q", role)
}

// UpsertUser создаёт или обновляет одного пользователя, не трогая остальных. Нужен нескольким репликам сервиса:
// Save заменяет набор целиком и затёр бы пользователей, созданных на другой реплике. Новый пользователь и тот, кому
// сменили глобальную роль, получает ту же роль в команде «Default» (как в Save).
func (b userBackend) UpsertUser(u ext.UserRecord) error {
	if err := b.s.ready(); err != nil {
		return err
	}
	if err := validRole(u.Role); err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	err := pgx.BeginFunc(ctx, b.s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x6B756274)); err != nil {
			return err
		}
		var old string
		err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1 AND org_id = $2`, u.ID, b.s.orgID).Scan(&old)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := upsertUser(ctx, tx, b.s.orgID, u, time.Now()); err != nil {
			return err
		}
		if old == u.Role {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO memberships (user_id, team_id, role) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, team_id) DO UPDATE SET role = EXCLUDED.role`, u.ID, b.s.teamID, u.Role)
		return err
	})
	b.s.invalidateAccess()
	return err
}

// TouchLogin записывает время входа и (если passwordHash не пуст) обновлённый хэш пароля; остальные поля
// пользователя, возможно изменённые на другой реплике, не затрагиваются.
func (b userBackend) TouchLogin(id string, at time.Time, passwordHash string) error {
	if err := b.s.ready(); err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	_, err := b.s.pool.Exec(ctx, `
		UPDATE users SET last_login_at = $2, password_hash = CASE WHEN $3::text <> '' THEN $3::text ELSE password_hash END
		WHERE id = $1 AND org_id = $4`, id, at, passwordHash, b.s.orgID)
	return err
}

// DeleteUser удаляет пользователя (сессии, членства и записи, зависящие от него, удаляет БД каскадом).
func (b userBackend) DeleteUser(id string) error {
	if err := b.s.ready(); err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	_, err := b.s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1 AND org_id = $2`, id, b.s.orgID)
	b.s.invalidateAccess()
	return err
}

// SaveSettings записывает настройки безопасности.
func (b userBackend) SaveSettings(st ext.SecuritySettings) error {
	if err := b.s.ready(); err != nil {
		return err
	}
	raw, err := json.Marshal(securityJSON{IdleTimeoutMinutes: st.IdleTimeoutMinutes})
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, settingsKeySecurity, raw)
	return err
}
