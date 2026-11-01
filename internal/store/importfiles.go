// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/24DSO-labs/kubron/internal/pgstore"
	"github.com/24DSO-labs/kubron/pkg/ext"
)

// ImportReport — итог переноса данных из файлового хранилища в PostgreSQL.
type ImportReport struct {
	Users, Clusters, Tokens, AuditEvents int
	// Notes — что пропущено и почему (для вывода администратору).
	Notes []string
}

// ImportFiles переносит данные файлового хранилища (десктоп, личный режим или сервер на файлах) в PostgreSQL
// (задача 3.1d). Источник только читается: исходные файлы не меняются и остаются резервной копией.
// Операция идемпотентна и ничего не затирает:
//   - пользователи и журнал переносятся только в пустые таблицы (иначе повтор дублировал бы записи);
//   - кластеры, которые уже есть в БД, пропускаются вместе с токенами;
//   - всё попадает в организацию и команду «Default».
func ImportFiles(ctx context.Context, src Store, dst *pgstore.Store) (ImportReport, error) {
	var rep ImportReport
	have, err := dst.Counts(ctx)
	if err != nil {
		return rep, err
	}

	// Пользователи.
	data, err := src.Users().Load()
	if err != nil {
		return rep, fmt.Errorf("read users: %w", err)
	}
	switch {
	case len(data.Users) == 0:
	case have.Users > 0:
		rep.Notes = append(rep.Notes, "users skipped: the database already has users")
	default:
		out := &ext.UserData{Settings: ext.SecuritySettings{IdleTimeoutMinutes: data.Settings.IdleTimeoutMinutes}}
		for _, u := range data.Users {
			out.Users = append(out.Users, ext.UserRecord{
				ID: u.ID, Username: u.Username, Role: string(u.Role), PasswordHash: u.PasswordHash,
				Disabled: u.Disabled, MustChangePassword: u.MustChangePassword,
				CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, LastLoginAt: u.LastLoginAt,
			})
		}
		if err := dst.Users().Save(out); err != nil {
			return rep, err
		}
		rep.Users = len(out.Users)
	}

	// Кластеры и токены Argo CD.
	clusters := pgClusters{dst.Clusters()}
	list, err := src.Clusters().List()
	if err != nil {
		return rep, fmt.Errorf("read clusters: %w", err)
	}
	for i := range list {
		m := list[i]
		if _, err := dst.Clusters().Get(m.ID); err == nil {
			rep.Notes = append(rep.Notes, "cluster skipped, already exists: "+m.ID)
			continue
		} else if !errors.Is(err, pgstore.ErrClusterNotFound) {
			return rep, err
		}
		kc, err := src.Clusters().Kubeconfig(m.ID)
		if err != nil {
			return rep, err
		}
		if err := clusters.Save(&m, kc); err != nil {
			return rep, err
		}
		rep.Clusters++
		switch tok, err := src.ArgoTokens().Get(m.ID); {
		case errors.Is(err, ext.ErrNotFound):
		case err != nil:
			return rep, err
		default:
			if err := dst.ArgoTokens().Put(m.ID, tok); err != nil {
				return rep, err
			}
			rep.Tokens++
		}
	}

	// Журнал аудита: файл читается от новых к старым, пишем от старых к новым.
	var lines [][]byte
	if err := src.Audit().ScanNewestFirst(func(line []byte) bool {
		lines = append(lines, append([]byte(nil), line...))
		return true
	}); err != nil {
		return rep, fmt.Errorf("read audit log: %w", err)
	}
	switch {
	case len(lines) == 0:
	case have.AuditEvents > 0:
		rep.Notes = append(rep.Notes, "audit log skipped: the database already has events")
	default:
		for i := len(lines) - 1; i >= 0; i-- {
			if err := dst.Audit().Append(lines[i]); err != nil {
				return rep, err
			}
			rep.AuditEvents++
		}
	}
	return rep, nil
}
