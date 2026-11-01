// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package pgstore — хранилище сервиса на PostgreSQL (ADR-0007): схема и миграции (3.1a), пользователи,
// журнал аудита, токены Argo CD и кластеры (3.1b). Секреты шифруются пакетом secretbox (3.1c).
//
// Пакет не зависит от internal/k8s и internal/app: кластеры отдаются собственными типами, к k8s.ClusterStore
// их приводит тонкий адаптер в internal/store.
package pgstore

import (
	"embed"

	"github.com/24DSO-labs/kubron/internal/migrate"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations возвращает встроенные миграции схемы.
func Migrations() ([]migrate.Migration, error) {
	return migrate.Load(migrationsFS, "migrations")
}
