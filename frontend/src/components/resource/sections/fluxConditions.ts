// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { FluxCondition } from '../../../types/flux'

export function conditionTone(c: Pick<FluxCondition, 'type' | 'status'>): string {
  const on = c.status === 'True'
  const off = c.status === 'False'
  if (c.type === 'Stalled') return on ? 'rd-err' : 'rd-unk'
  if (c.type === 'Reconciling') return on ? 'rd-warn' : 'rd-unk'
  if (on) return 'rd-ok'
  if (off) return 'rd-err'
  return 'rd-unk'
}
