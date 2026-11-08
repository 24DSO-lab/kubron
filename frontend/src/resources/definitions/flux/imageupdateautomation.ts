// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { t } from '../../../i18n'
import { callApp } from '../../wails'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { shortRevision, sourceRefLabel } from '../../flux'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxImageUpdateAutomationInfo } from '../../../types/flux'
import { dash, imageActions, imageConditionsTab, imageStateBadge, imageStateColor, imageStateLabel, imageStateRank, imageTime } from './imagerepository'

/** ImageUpdateAutomation (Flux CD): источник, ветка, последний запуск и коммит */
export const fluxImageUpdateAutomation = defineResource<FluxImageUpdateAutomationInfo>({
  id: 'flux-imageupdateautomation',
  kind: 'ImageUpdateAutomation',
  view: 'flux-imageupdateautomations',
  title: 'ImageUpdateAutomations',
  namespaced: true,
  nav: { group: 'flux-images', label: 'ImageUpdateAutomations' },
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,
  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: imageStateLabel, colorFn: imageStateColor, sortValue: imageStateRank },
    { key: 'sourceRef', label: 'fluxImages.columns.source', formatter: a => sourceRefLabel(a.sourceRef) || dash },
    { key: 'branch', label: 'fluxImages.columns.branch', formatter: a => a.branch || dash },
    { key: 'lastRunAt', label: 'fluxImages.columns.lastRun', formatter: a => imageTime(a.lastRunAt) ?? dash, sortValue: a => Date.parse(a.lastRunAt) || null },
    { key: 'lastPushCommit', label: 'fluxImages.columns.commit', formatter: a => shortRevision(a.lastPushCommit) || dash },
    ageCol,
  ],
  properties: [
    { label: 'common.status', value: imageStateLabel, badge: imageStateBadge },
    { label: 'defs.columns.message', value: a => a.message, hideEmpty: true },
    { label: 'fluxImages.columns.source', value: a => sourceRefLabel(a.sourceRef), hideEmpty: true },
    { label: 'fluxImages.columns.branch', value: a => a.branch, hideEmpty: true },
    { label: 'fluxImages.columns.lastRun', value: a => imageTime(a.lastRunAt), hideEmpty: true },
    { label: 'fluxImages.columns.commit', value: a => a.lastPushCommit, hideEmpty: true },
    { label: 'fluxImages.props.interval', value: a => a.interval, hideEmpty: true },
    { label: 'fluxImages.props.suspended', value: a => (a.suspended ? t('common.yes') : null), hideEmpty: true },
  ],
  tabs: [imageConditionsTab],
  actions: imageActions<FluxImageUpdateAutomationInfo>('imageupdateautomations'),
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxImageAutomation', ns)).automations ?? []) as unknown as FluxImageUpdateAutomationInfo[],
  },
})
