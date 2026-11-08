// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { t } from '../../../i18n'
import { formatDateTime } from '../../../i18n/format'
import { callApp } from '../../wails'
import { badgeClass, statusColor } from '../../status'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxStateRank } from '../../flux'
import { reconcileFlux, setFluxSuspended } from '../../fluxActions'
import { useFlux } from '../../../composables/useFlux'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import { Perm } from '../../../types/auth'
import type { ActionDef } from '../../types'
import type { FluxImageRepositoryInfo, FluxObjectInfo } from '../../../types/flux'
import FluxConditionsSection from '../../../components/resource/sections/FluxConditionsSection.vue'

const KNOWN = ['Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled']
export const dash = '—'

/** Общие для трёх видов Image automation помощники: состояние, время, вкладка условий, действия */
type S = Pick<FluxObjectInfo, 'state'>
export const imageStateLabel = (o: S) => t(`flux.state.${KNOWN.includes(o.state) ? o.state : 'Unknown'}`)
const statusKey = (o: S) => (o.state === 'Reconciling' ? 'progressing' : o.state === 'Stalled' ? 'failed' : o.state)
export const imageStateColor = (o: S) => statusColor(statusKey(o))
export const imageStateBadge = (o: S) => badgeClass(statusKey(o))
export const imageStateRank = (o: S) => fluxStateRank(o.state)
export const imageTime = (v?: string) => (v && !Number.isNaN(Date.parse(v)) ? formatDateTime(v) : null)
export const imageConditionsTab = { id: 'fluxConditions', label: 'defs.columns.conditions', component: FluxConditionsSection }

/** Reconcile, Suspend/Resume и Edit YAML (ImageRepository и ImageUpdateAutomation); ImagePolicy действий не имеет */
export function imageActions<T extends FluxObjectInfo>(plural: string): ActionDef<T>[] {
  const yamlKind = (kind: string) => `cr:image.toolkit.fluxcd.io/${useFlux().fluxStatus.value.versions?.[kind] || 'v1'}/${plural}`
  return [
    {
      id: 'reconcile', label: 'fluxImages.menu.reconcile', icon: 'restart', permission: Perm.Operate,
      run: o => reconcileFlux([o as unknown as FluxObjectInfo], { withSource: false }),
    },
    {
      id: 'suspend', label: 'defs.menu.suspend', icon: 'pause', permission: Perm.Write,
      when: o => !o.suspended,
      run: o => setFluxSuspended([o as unknown as FluxObjectInfo], true),
    },
    {
      id: 'resume', label: 'defs.menu.resume', icon: 'play', permission: Perm.Write,
      when: o => !!o.suspended,
      run: o => setFluxSuspended([o as unknown as FluxObjectInfo], false),
    },
    {
      id: 'editYaml', label: 'fluxImages.menu.editYaml', permission: Perm.Write,
      run: (o, ctx) => ctx.edit(yamlKind(o.kind), o.namespace, o.name),
    },
  ]
}

/** ImageRepository (Flux CD): образ, число тегов, последний скан */
export const fluxImageRepository = defineResource<FluxImageRepositoryInfo>({
  id: 'flux-imagerepository',
  kind: 'ImageRepository',
  view: 'flux-imagerepositories',
  title: 'ImageRepositories',
  namespaced: true,
  nav: { group: 'flux-images', label: 'ImageRepositories' },
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,
  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: imageStateLabel, colorFn: imageStateColor, sortValue: imageStateRank },
    { key: 'image', label: 'fluxImages.columns.image', formatter: r => r.image || dash },
    { key: 'tagCount', label: 'fluxImages.columns.tags', formatter: r => String(r.tagCount ?? 0), sortValue: r => r.tagCount ?? 0 },
    { key: 'lastScanAt', label: 'fluxImages.columns.lastScan', formatter: r => imageTime(r.lastScanAt) ?? dash, sortValue: r => Date.parse(r.lastScanAt) || null },
    ageCol,
  ],
  properties: [
    { label: 'common.status', value: imageStateLabel, badge: imageStateBadge },
    { label: 'defs.columns.message', value: r => r.message, hideEmpty: true },
    { label: 'fluxImages.columns.image', value: r => r.image, hideEmpty: true },
    { label: 'fluxImages.columns.tags', value: r => r.tagCount, hideEmpty: true },
    { label: 'fluxImages.columns.lastScan', value: r => imageTime(r.lastScanAt), hideEmpty: true },
    { label: 'fluxImages.props.interval', value: r => r.interval, hideEmpty: true },
    { label: 'fluxImages.props.suspended', value: r => (r.suspended ? t('common.yes') : null), hideEmpty: true },
  ],
  tabs: [imageConditionsTab],
  actions: imageActions<FluxImageRepositoryInfo>('imagerepositories'),
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxImageAutomation', ns)).repositories ?? []) as unknown as FluxImageRepositoryInfo[],
  },
})
