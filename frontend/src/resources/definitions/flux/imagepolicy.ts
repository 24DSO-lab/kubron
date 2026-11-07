// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxImagePolicyInfo } from '../../../types/flux'

/** ImagePolicy (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.8 */
export const fluxImagePolicy = defineResource<FluxImagePolicyInfo>({
  id: 'flux-imagepolicy',
  kind: 'ImagePolicy',
  view: 'flux-imagepolicies',
  title: 'ImagePolicies',
  namespaced: true,
  nav: { group: 'flux-images', label: 'ImagePolicies' },
  // YAML и правка идут через cr:group/version/plural (задача U.8), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxImageAutomation', ns)).policies) as unknown as FluxImagePolicyInfo[],
  },
})
