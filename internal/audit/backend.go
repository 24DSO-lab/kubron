// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import "github.com/24DSO-labs/kubron/pkg/ext"

// Backend — хранилище записей журнала. Реализации: файлы с ротацией (десктоп) и,
// позже, PostgreSQL (сервер, ADR-0007). Интерфейс публичный (pkg/ext). Вызывается под блокировкой Logger.
type Backend = ext.AuditBackend

// NewFileBackend возвращает файловое хранилище журнала в каталоге dir (с ротацией).
func NewFileBackend(dir string) Backend { return newFileBackend(dir) }
