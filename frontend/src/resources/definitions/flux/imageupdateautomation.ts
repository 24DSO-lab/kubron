// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxImageUpdateAutomationInfo } from '../../../types/flux'

/** ImageUpdateAutomation (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.8 */
export const fluxImageUpdateAutomation = defineResource<FluxImageUpdateAutomationInfo>({
  id: 'flux-imageupdateautomation',
  kind: 'ImageUpdateAutomation',
  view: 'flux-imageupdateautomations',
  title: 'ImageUpdateAutomations',
  namespaced: true,
  nav: { group: 'flux-images', label: 'ImageUpdateAutomations' },
  // YAML и правка идут через cr:group/version/plural (задача U.8), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxImageAutomation', ns)).automations) as unknown as FluxImageUpdateAutomationInfo[],
  },
})
