// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { FluxHelmHistoryEntry } from '../../../types/flux'

/** Новые записи первыми; входной массив не мутируется. */
export function sortHistory(
  history: readonly FluxHelmHistoryEntry[] | null | undefined,
): FluxHelmHistoryEntry[] {
  return [...(history ?? [])].filter(Boolean).sort((a, b) => (b.version ?? 0) - (a.version ?? 0))
}
