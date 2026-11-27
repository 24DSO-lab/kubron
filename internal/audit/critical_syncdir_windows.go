// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package audit

// syncCriticalDir на Windows — no-op: каталог нельзя открыть и сбросить через Sync; метаданные
// создания файла NTFS журналирует сама, а содержимое журнала сбрасывается file Sync.
func syncCriticalDir(string) error { return nil }
