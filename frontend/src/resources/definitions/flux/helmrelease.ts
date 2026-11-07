// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxHelmReleaseInfo } from '../../../types/flux'

/** HelmRelease (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.5 */
export const fluxHelmRelease = defineResource<FluxHelmReleaseInfo>({
  id: 'flux-helmrelease',
  kind: 'HelmRelease',
  view: 'flux-helmreleases',
  title: 'HelmReleases',
  namespaced: true,
  nav: { group: 'flux', label: 'HelmReleases' },
  // YAML и правка идут через cr:group/version/plural (задача U.5), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: ns => callApp('GetFluxHelmReleases', ns) as Promise<FluxHelmReleaseInfo[]>,
  },
})
