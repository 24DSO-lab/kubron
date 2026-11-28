// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package authorization

// syncDir на Windows — no-op: каталог нельзя открыть и сбросить через Sync; os.Rename заменяет файл
// атомарно (MoveFileEx с REPLACE_EXISTING), содержимое сброшено file Sync до rename.
func syncDir(string) error { return nil }
