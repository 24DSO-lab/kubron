// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxImagePolicyInfo } from '../../../types/flux'
import { dash, imageConditionsTab, imageStateBadge, imageStateColor, imageStateLabel, imageStateRank } from './imagerepository'

/** ImagePolicy (Flux CD): только чтение, действий нет (ADR-0023, раздел 6). Правило и последний выбранный образ */
export const fluxImagePolicy = defineResource<FluxImagePolicyInfo>({
  id: 'flux-imagepolicy',
  kind: 'ImagePolicy',
  view: 'flux-imagepolicies',
  title: 'ImagePolicies',
  namespaced: true,
  nav: { group: 'flux-images', label: 'ImagePolicies' },
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,
  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: imageStateLabel, colorFn: imageStateColor, sortValue: imageStateRank },
    { key: 'imageRepository', label: 'fluxImages.columns.repository', formatter: p => p.imageRepository || dash },
    { key: 'policy', label: 'fluxImages.columns.policy', formatter: p => p.policy || dash },
    { key: 'latestImage', label: 'fluxImages.columns.latestImage', formatter: p => p.latestImage || dash },
    ageCol,
  ],
  properties: [
    { label: 'common.status', value: imageStateLabel, badge: imageStateBadge },
    { label: 'defs.columns.message', value: p => p.message, hideEmpty: true },
    { label: 'fluxImages.columns.repository', value: p => p.imageRepository, hideEmpty: true },
    { label: 'fluxImages.columns.policy', value: p => p.policy, hideEmpty: true },
    { label: 'fluxImages.columns.latestImage', value: p => p.latestImage, hideEmpty: true },
  ],
  tabs: [imageConditionsTab],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxImageAutomation', ns)).policies ?? []) as unknown as FluxImagePolicyInfo[],
  },
})
