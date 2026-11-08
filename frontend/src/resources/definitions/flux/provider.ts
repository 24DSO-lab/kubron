// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxProviderInfo } from '../../../types/flux'
import { dash, notifyConditionsTab, notifyStateBadge, notifyStateColor, notifyStateLabel, notifyStateRank, notifyTime } from './alert'

/**
 * Provider (Flux CD): только чтение. Адрес приходит из Go уже без пути, запроса и userinfo (схема и хост),
 * из secretRef — только имя. YAML не предлагается: spec.address в нём полный.
 */
export const fluxProvider = defineResource<FluxProviderInfo>({
  id: 'flux-provider',
  kind: 'Provider',
  view: 'flux-providers',
  title: 'Providers',
  namespaced: true,
  nav: { group: 'flux-notifications', label: 'Providers' },
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,
  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: notifyStateLabel, colorFn: notifyStateColor, sortValue: notifyStateRank },
    { key: 'type', label: 'fluxNotify.columns.type', formatter: p => p.type || dash },
    { key: 'channel', label: 'fluxNotify.columns.channel', formatter: p => p.channel || dash },
    { key: 'address', label: 'fluxNotify.columns.address', formatter: p => p.address || dash },
    ageCol,
  ],
  properties: [
    { label: 'common.status', value: notifyStateLabel, badge: notifyStateBadge },
    { label: 'defs.columns.message', value: p => p.message, hideEmpty: true },
    { label: 'fluxNotify.columns.type', value: p => p.type, hideEmpty: true },
    { label: 'fluxNotify.columns.channel', value: p => p.channel, hideEmpty: true },
    { label: 'fluxNotify.columns.address', value: p => p.address, hideEmpty: true },
    { label: 'fluxNotify.props.secretRef', value: p => p.secretRef, hideEmpty: true },
    { label: 'fluxNotify.props.lastReconcile', value: p => notifyTime(p.lastReconcileAt), hideEmpty: true },
  ],
  tabs: [notifyConditionsTab],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxNotifications', ns)).providers ?? []) as unknown as FluxProviderInfo[],
  },
})
