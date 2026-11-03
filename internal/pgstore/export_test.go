// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Помощники для внешних тестов (pgstore_test): сквозные проверки через internal/store и internal/k8s живут в
// каталоге pgstore, чтобы идти в одном бинарнике с остальными PG-тестами (они пересоздают схему public).

// NewTestStore — одноразовая БД из KUBRON_TEST_DATABASE_URL с накатанными миграциями (как newTestStore).
func NewTestStore(t *testing.T, keyStr string) *Store { return newTestStore(t, keyStr) }

// QueryRowForTest выполняет запрос напрямую, чтобы проверить, что лежит в таблицах.
func (s *Store) QueryRowForTest(sql string, args ...any) pgx.Row {
	return s.pool.QueryRow(context.Background(), sql, args...)
}
