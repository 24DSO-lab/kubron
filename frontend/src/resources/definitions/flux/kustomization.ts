// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxKustomizationInfo } from '../../../types/flux'

/** Kustomization (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.4 */
export const fluxKustomization = defineResource<FluxKustomizationInfo>({
  id: 'flux-kustomization',
  kind: 'Kustomization',
  view: 'flux-kustomizations',
  title: 'Kustomizations',
  namespaced: true,
  nav: { group: 'flux', label: 'Kustomizations' },
  // YAML и правка идут через cr:group/version/plural (задача U.4), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: ns => callApp('GetFluxKustomizations', ns) as Promise<FluxKustomizationInfo[]>,
  },
})
