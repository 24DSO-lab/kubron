// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxImageRepositoryInfo } from '../../../types/flux'

/** ImageRepository (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.8 */
export const fluxImageRepository = defineResource<FluxImageRepositoryInfo>({
  id: 'flux-imagerepository',
  kind: 'ImageRepository',
  view: 'flux-imagerepositories',
  title: 'ImageRepositories',
  namespaced: true,
  nav: { group: 'flux-images', label: 'ImageRepositories' },
  // YAML и правка идут через cr:group/version/plural (задача U.8), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxImageAutomation', ns)).repositories) as unknown as FluxImageRepositoryInfo[],
  },
})
