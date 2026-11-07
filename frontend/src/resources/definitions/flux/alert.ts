// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxAlertInfo } from '../../../types/flux'

/** Alert (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.7 */
export const fluxAlert = defineResource<FluxAlertInfo>({
  id: 'flux-alert',
  kind: 'Alert',
  view: 'flux-alerts',
  title: 'Alerts',
  namespaced: true,
  nav: { group: 'flux-notifications', label: 'Alerts' },
  // YAML и правка идут через cr:group/version/plural (задача U.7), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxNotifications', ns)).alerts) as unknown as FluxAlertInfo[],
  },
})
