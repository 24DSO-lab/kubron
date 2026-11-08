// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineComponent, h } from 'vue'
import { defineResource } from '../../types'
import { t } from '../../../i18n'
import { formatDateTime } from '../../../i18n/format'
import { callApp } from '../../wails'
import { badgeClass, statusColor } from '../../status'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { chartLabel, fluxStateRank, shortRevision, sourceRefLabel } from '../../flux'
import { reconcileFlux, requestFluxDelete, setFluxSuspended } from '../../fluxActions'
import { useFlux } from '../../../composables/useFlux'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import { Perm } from '../../../types/auth'
import type { FluxHelmReleaseInfo } from '../../../types/flux'
import FluxConditionsSection from '../../../components/resource/sections/FluxConditionsSection.vue'
import FluxHistoryTab from '../../../components/resource/sections/FluxHistoryTab.vue'
import FluxInventoryTab from '../../../components/resource/sections/FluxInventoryTab.vue'
import FluxSourceRefSection from '../../../components/resource/sections/FluxSourceRefSection.vue'
import FluxGraphTab from '../../../components/resource/sections/FluxGraphTab.vue'

const GROUP = 'helm.toolkit.fluxcd.io'
const KNOWN = ['Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled']

type S = Pick<FluxHelmReleaseInfo, 'state'>
const stateLabel = (r: S) => t(`flux.state.${KNOWN.includes(r.state) ? r.state : 'Unknown'}`)
/** Reconciling — «в процессе», Stalled — как ошибка; остальное по общим правилам status.ts */
const statusKey = (r: S) => (r.state === 'Reconciling' ? 'progressing' : r.state === 'Stalled' ? 'failed' : r.state)
const stateColor = (r: S) => statusColor(statusKey(r))
const stateBadge = (r: S) => badgeClass(statusKey(r))
const dash = '—'

const yamlKind = () => `cr:${GROUP}/${useFlux().fluxStatus.value.versions?.HelmRelease || 'v2'}/helmreleases`

/** Чарт: из spec.chart (имя@версия), иначе из chartRef (OCIRepository / HelmChart) */
export const helmChart = (r: Pick<FluxHelmReleaseInfo, 'chart' | 'chartVersion' | 'chartRef'>) =>
  chartLabel(r.chart, r.chartVersion) || sourceRefLabel(r.chartRef)

/** Без status.inventory (Flux до 2.8) вкладка ресурсов недоступна: вместо запроса графа — пояснение */
export const HelmResourcesTab = defineComponent({
  name: 'FluxHelmResourcesTab',
  props: { resource: { type: Object, default: () => ({}) } },
  emits: ['open'],
  setup(props, { emit }) {
    return () => {
      const r = props.resource as Partial<FluxHelmReleaseInfo>
      if (!r.hasInventory) {
        return h('div', { class: 'rd-empty', 'data-testid': 'flux-helm-noinventory' }, t('fluxHelm.noInventory'))
      }
      return h(FluxInventoryTab, { resource: r, onOpen: (p: unknown) => emit('open', p) })
    }
  },
})

/** HelmRelease (Flux CD): список, детали, история, ресурсы, источник, граф, действия */
export const fluxHelmRelease = defineResource<FluxHelmReleaseInfo>({
  id: 'flux-helmrelease',
  kind: 'HelmRelease',
  view: 'flux-helmreleases',
  title: 'HelmReleases',
  namespaced: true,
  nav: { group: 'flux', label: 'HelmReleases' },
  // YAML не по id ресурса, а через cr:-вид: см. действие editYaml
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,

  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: stateLabel, colorFn: stateColor, sortValue: r => fluxStateRank(r.state) },
    { key: 'chart', label: 'fluxHelm.columns.chart', formatter: r => helmChart(r) || dash },
    { key: 'releaseName', label: 'fluxHelm.columns.release', formatter: r => r.releaseName || dash },
    { key: 'targetNamespace', label: 'fluxHelm.columns.targetNamespace', formatter: r => r.targetNamespace || dash },
    { key: 'revision', label: 'defs.columns.revision', formatter: r => shortRevision(r.revision) || dash },
    ageCol,
  ],

  properties: [
    { label: 'common.status', value: stateLabel, badge: stateBadge },
    { label: 'defs.columns.message', value: r => r.message, hideEmpty: true },
    { label: 'fluxHelm.columns.chart', value: r => helmChart(r), hideEmpty: true },
    { label: 'fluxHelm.columns.release', value: r => r.releaseName, hideEmpty: true },
    { label: 'fluxHelm.columns.targetNamespace', value: r => r.targetNamespace, hideEmpty: true },
    { label: 'fluxHelm.props.storageNamespace', value: r => r.storageNamespace, hideEmpty: true },
    { label: 'defs.columns.revision', value: r => r.revision, hideEmpty: true },
    { label: 'fluxHelm.props.interval', value: r => r.interval, hideEmpty: true },
    { label: 'fluxHelm.props.remote', value: r => (r.remote ? t('common.yes') : null), hideEmpty: true },
    { label: 'fluxHelm.props.suspended', value: r => (r.suspended ? t('common.yes') : null), hideEmpty: true },
    {
      label: 'fluxHelm.props.lastReconcile',
      value: r => (r.lastReconcileAt && !Number.isNaN(Date.parse(r.lastReconcileAt)) ? formatDateTime(r.lastReconcileAt) : null),
      hideEmpty: true,
    },
    { label: 'fluxHelm.props.managed', value: r => (r.hasInventory ? r.inventoryCount : null), hideEmpty: true },
  ],

  tabs: [
    { id: 'fluxConditions', label: 'defs.columns.conditions', component: FluxConditionsSection },
    { id: 'fluxHistory', label: 'defs.tabs.history', component: FluxHistoryTab },
    { id: 'fluxResources', label: 'defs.tabs.resources', component: HelmResourcesTab },
    { id: 'fluxSource', label: 'fluxHelm.tabs.source', component: FluxSourceRefSection },
    { id: 'fluxGraph', label: 'fluxHelm.tabs.graph', component: FluxGraphTab },
  ],

  actions: [
    {
      id: 'reconcile', label: 'fluxHelm.menu.reconcile', icon: 'restart', permission: Perm.Operate,
      run: r => reconcileFlux([r as FluxHelmReleaseInfo], { withSource: false }),
    },
    {
      id: 'reconcileWithSource', label: 'fluxHelm.menu.reconcileWithSource', icon: 'refresh', permission: Perm.Operate,
      run: r => reconcileFlux([r as FluxHelmReleaseInfo], { withSource: true }),
    },
    {
      id: 'suspend', label: 'defs.menu.suspend', icon: 'pause', permission: Perm.Write,
      when: r => !r.suspended,
      run: r => setFluxSuspended([r as FluxHelmReleaseInfo], true),
    },
    {
      id: 'resume', label: 'defs.menu.resume', icon: 'play', permission: Perm.Write,
      when: r => !!r.suspended,
      run: r => setFluxSuspended([r as FluxHelmReleaseInfo], false),
    },
    {
      id: 'editYaml', label: 'fluxHelm.menu.editYaml', permission: Perm.Write,
      run: (r, ctx) => ctx.edit(yamlKind(), r.namespace, r.name),
    },
    {
      id: 'delete', label: 'common.delete', icon: 'delete', danger: true, permission: Perm.Write,
      run: r => requestFluxDelete(r as FluxHelmReleaseInfo),
    },
  ],

  api: {
    list: ns => callApp('GetFluxHelmReleases', ns) as Promise<FluxHelmReleaseInfo[]>,
    // remove не объявлен: удаление идёт через действие delete (подтверждение вводом имени)
  },
})
