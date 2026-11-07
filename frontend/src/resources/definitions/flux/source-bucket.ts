// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxSourceInfo } from '../../../types/flux'

/** Источники приходят одним списком (GetFluxSources): определение берёт свой вид */
const sources = async (ns: string[], kind: string): Promise<FluxSourceInfo[]> =>
  (await callApp('GetFluxSources', ns)).filter(s => s.kind === kind) as unknown as FluxSourceInfo[]

/** Bucket (Flux CD). Заглушка задачи 0.2: колонки, свойства и вкладки заполняет задача U.6 */
export const fluxBucket = defineResource<FluxSourceInfo>({
  id: 'flux-bucket',
  kind: 'Bucket',
  view: 'flux-buckets',
  title: 'Buckets',
  namespaced: true,
  nav: { group: 'flux-sources', label: 'Buckets' },
  // YAML и правка идут через cr:group/version/plural (задача U.6), поэтому пока не предлагаем Edit YAML
  editable: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,
  columns: [flatNameCol, flatNsCol, { key: 'state', label: 'common.status' }, ageCol],
  api: {
    list: ns => sources(ns, 'Bucket'),
  },
})
