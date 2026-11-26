// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { investigate } from '../composables/incidentLaunch'
import { Perm } from '../types/auth'
import type { TargetKind } from '../types/incident'
import type { ActionDef } from './types'
import { itemName, itemNs } from './utils'

/**
 * Кнопка «Расследовать» в шапке карточки ресурса. Открытие расследования требует только права чтения
 * (проверку делает бэкенд). Вид передаётся явно: он совпадает с видом из internal/diagnose, а не всегда с def.kind
 * (например, у HPA def.kind = 'HPA').
 */
export const investigateAction = (kind: TargetKind): ActionDef => ({
  id: 'investigate',
  permission: Perm.Read,
  label: 'defs.menu.investigate',
  run: (item, ctx) => {
    ctx.closeDetail()
    // Nodes are cluster-scoped; ignore any accidental namespace on the supplied object.
    const namespace = kind === 'Node' ? '' : itemNs(item)
    investigate(kind, namespace, itemName(item))
  },
})
