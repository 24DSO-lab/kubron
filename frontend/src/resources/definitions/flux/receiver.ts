// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxReceiverInfo } from '../../../types/flux'
import { dash, notifyConditionsTab, notifyStateBadge, notifyStateColor, notifyStateLabel, notifyStateRank, notifyTime, refList } from './alert'

/**
 * Receiver (Flux CD): только чтение. Путь вебхука (status.webhookPath) не показывается и в DTO не приходит,
 * поэтому YAML не предлагается: в нём этот путь есть.
 */
export const fluxReceiver = defineResource<FluxReceiverInfo>({
  id: 'flux-receiver',
  kind: 'Receiver',
  view: 'flux-receivers',
  title: 'Receivers',
  namespaced: true,
  nav: { group: 'flux-notifications', label: 'Receivers' },
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,
  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: notifyStateLabel, colorFn: notifyStateColor, sortValue: notifyStateRank },
    { key: 'type', label: 'fluxNotify.columns.type', formatter: r => r.type || dash },
    { key: 'resources', label: 'fluxNotify.columns.resources', formatter: r => refList(r.resources) || dash },
    ageCol,
  ],
  properties: [
    { label: 'common.status', value: notifyStateLabel, badge: notifyStateBadge },
    { label: 'defs.columns.message', value: r => r.message, hideEmpty: true },
    { label: 'fluxNotify.columns.type', value: r => r.type, hideEmpty: true },
    { label: 'fluxNotify.columns.resources', value: r => refList(r.resources), multiline: true, hideEmpty: true },
    { label: 'fluxNotify.props.secretRef', value: r => r.secretRef, hideEmpty: true },
    { label: 'fluxNotify.props.lastReconcile', value: r => notifyTime(r.lastReconcileAt), hideEmpty: true },
  ],
  tabs: [notifyConditionsTab],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxNotifications', ns)).receivers ?? []) as unknown as FluxReceiverInfo[],
  },
})
