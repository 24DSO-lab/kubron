// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { t } from '../../../i18n'
import { formatDateTime } from '../../../i18n/format'
import { callApp } from '../../wails'
import { badgeClass, statusColor } from '../../status'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxStateRank, shortRevision, sourceRefLabel } from '../../flux'
import { reconcileFlux, requestFluxDelete, setFluxSuspended } from '../../fluxActions'
import { useFlux } from '../../../composables/useFlux'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import { Perm } from '../../../types/auth'
import type { FluxKustomizationInfo } from '../../../types/flux'
import FluxConditionsSection from '../../../components/resource/sections/FluxConditionsSection.vue'
import FluxInventoryTab from '../../../components/resource/sections/FluxInventoryTab.vue'
import FluxSourceRefSection from '../../../components/resource/sections/FluxSourceRefSection.vue'
import FluxGraphTab from '../../../components/resource/sections/FluxGraphTab.vue'

const GROUP = 'kustomize.toolkit.fluxcd.io'

/** Подпись состояния: значения вне контракта показываются как Unknown (как в FluxStateBadge) */
const stateLabel = (k: Pick<FluxKustomizationInfo, 'state'>) =>
  t(`flux.state.${['Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled'].includes(k.state) ? k.state : 'Unknown'}`)

/** Reconciling показываем как «в процессе», Stalled — как ошибку; остальное по общим правилам status.ts */
const statusKey = (k: Pick<FluxKustomizationInfo, 'state'>) =>
  k.state === 'Reconciling' ? 'progressing' : k.state === 'Stalled' ? 'failed' : k.state
const stateColor = (k: Pick<FluxKustomizationInfo, 'state'>) => statusColor(statusKey(k))
const stateBadge = (k: Pick<FluxKustomizationInfo, 'state'>) => badgeClass(statusKey(k))

const yesNo = (v: boolean) => (v ? t('common.yes') : t('common.no'))
const dash = '—'

/** id вида cr:group/version/plural для YAML; версия — та, что обслуживает кластер (FluxStatus), иначе v1 */
const yamlKind = () => `cr:${GROUP}/${useFlux().fluxStatus.value.versions?.Kustomization || 'v1'}/kustomizations`

/** Kustomization (Flux CD): список, детали, условия, ресурсы, источник, граф, действия */
export const fluxKustomization = defineResource<FluxKustomizationInfo>({
  id: 'flux-kustomization',
  kind: 'Kustomization',
  view: 'flux-kustomizations',
  title: 'Kustomizations',
  namespaced: true,
  nav: { group: 'flux', label: 'Kustomizations' },
  // YAML не по id ресурса (его нет в kindTable), а через cr:-вид: см. действие editYaml
  editable: false,
  // Удаление Flux — только по одному объекту и с подтверждением (ADR-0023, раздел 6)
  bulkDelete: false,
  autoRefresh: true,
  // Список перезагружается по событию flux:changed (informer), таймер остаётся страховкой
  refreshOn: () => fluxLiveTick.value,

  columns: [
    flatNameCol,
    flatNsCol,
    {
      key: 'state',
      label: 'common.status',
      formatter: stateLabel,
      colorFn: stateColor,
      sortValue: k => fluxStateRank(k.state),
    },
    { key: 'message', label: 'defs.columns.message', formatter: k => k.message || dash },
    { key: 'revision', label: 'defs.columns.revision', formatter: k => shortRevision(k.revision) || dash },
    { key: 'sourceRef', label: 'fluxKustomization.columns.source', formatter: k => sourceRefLabel(k.sourceRef) || dash },
    { key: 'path', label: 'fluxKustomization.columns.path', formatter: k => k.path || dash },
    { key: 'interval', label: 'fluxKustomization.columns.interval', formatter: k => k.interval || dash },
    { key: 'suspended', label: 'fluxKustomization.columns.suspended', formatter: k => yesNo(!!k.suspended) },
    ageCol,
  ],

  properties: [
    { label: 'common.status', value: stateLabel, badge: stateBadge },
    { label: 'defs.columns.message', value: k => k.message, hideEmpty: true },
    { label: 'defs.columns.revision', value: k => k.revision, hideEmpty: true },
    { label: 'fluxKustomization.props.lastAttempted', value: k => (k.lastAttemptedRevision !== k.revision ? k.lastAttemptedRevision : null), hideEmpty: true },
    { label: 'fluxKustomization.columns.source', value: k => sourceRefLabel(k.sourceRef), hideEmpty: true },
    { label: 'fluxKustomization.columns.path', value: k => k.path, hideEmpty: true },
    { label: 'fluxKustomization.columns.interval', value: k => k.interval, hideEmpty: true },
    { label: 'fluxKustomization.props.prune', value: k => yesNo(!!k.prune) },
    { label: 'fluxKustomization.props.wait', value: k => yesNo(!!k.wait) },
    { label: 'fluxKustomization.props.targetNamespace', value: k => k.targetNamespace, hideEmpty: true },
    { label: 'fluxKustomization.props.serviceAccount', value: k => k.serviceAccount, hideEmpty: true },
    { label: 'fluxKustomization.props.remote', value: k => (k.remote ? t('common.yes') : null), hideEmpty: true },
    { label: 'fluxKustomization.props.inventory', value: k => k.inventoryCount, hideEmpty: true },
    {
      label: 'fluxKustomization.props.lastReconcile',
      value: k => (k.lastReconcileAt && !Number.isNaN(Date.parse(k.lastReconcileAt)) ? formatDateTime(k.lastReconcileAt) : null),
      hideEmpty: true,
    },
    { label: 'fluxKustomization.columns.suspended', value: k => (k.suspended ? t('common.yes') : null), hideEmpty: true },
  ],

  tabs: [
    { id: 'fluxConditions', label: 'defs.columns.conditions', component: FluxConditionsSection },
    { id: 'fluxResources', label: 'defs.tabs.resources', component: FluxInventoryTab },
    { id: 'fluxSource', label: 'fluxKustomization.tabs.source', component: FluxSourceRefSection },
    { id: 'fluxGraph', label: 'fluxKustomization.tabs.graph', component: FluxGraphTab },
  ],

  actions: [
    {
      id: 'reconcile', label: 'fluxKustomization.menu.reconcile', icon: 'restart', permission: Perm.Operate,
      run: k => reconcileFlux([k as FluxKustomizationInfo], { withSource: false }),
    },
    {
      id: 'reconcileWithSource', label: 'fluxKustomization.menu.reconcileWithSource', icon: 'refresh', permission: Perm.Operate,
      run: k => reconcileFlux([k as FluxKustomizationInfo], { withSource: true }),
    },
    {
      id: 'suspend', label: 'defs.menu.suspend', icon: 'pause', permission: Perm.Write,
      when: k => !k.suspended,
      run: k => setFluxSuspended([k as FluxKustomizationInfo], true),
    },
    {
      id: 'resume', label: 'defs.menu.resume', icon: 'play', permission: Perm.Write,
      when: k => !!k.suspended,
      run: k => setFluxSuspended([k as FluxKustomizationInfo], false),
    },
    {
      id: 'editYaml', label: 'fluxKustomization.menu.editYaml', permission: Perm.Write,
      run: (k, ctx) => ctx.edit(yamlKind(), k.namespace, k.name),
    },
    {
      id: 'delete', label: 'common.delete', icon: 'delete', danger: true, permission: Perm.Write,
      run: k => requestFluxDelete(k as FluxKustomizationInfo),
    },
  ],

  api: {
    // Пустой namespace = все namespace; ограничение набором namespace делает бэкенд (parseScope)
    list: ns => callApp('GetFluxKustomizations', ns) as Promise<FluxKustomizationInfo[]>,
    // remove не объявлен намеренно: удаление идёт через действие delete (подтверждение вводом имени)
  },
})
