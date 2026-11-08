// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineComponent, h } from 'vue'
import { defineResource, type ColumnDef } from '../../types'
import { t } from '../../../i18n'
import { formatDateTime } from '../../../i18n/format'
import { callApp } from '../../wails'
import { badgeClass, statusColor } from '../../status'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { chartLabel, fluxStateRank, shortRevision } from '../../flux'
import { reconcileFlux, setFluxSuspended } from '../../fluxActions'
import { useFlux } from '../../../composables/useFlux'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import { Perm } from '../../../types/auth'
import type { FluxSourceInfo } from '../../../types/flux'
import FluxConditionsSection from '../../../components/resource/sections/FluxConditionsSection.vue'

const GROUP = 'source.toolkit.fluxcd.io'
const KNOWN = ['Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled']
const dash = '—'

type S = Pick<FluxSourceInfo, 'state'>
const stateLabel = (s: S) => t(`flux.state.${KNOWN.includes(s.state) ? s.state : 'Unknown'}`)
/** Reconciling — «в процессе», Stalled — как ошибка; остальное по общим правилам status.ts */
const statusKey = (s: S) => (s.state === 'Reconciling' ? 'progressing' : s.state === 'Stalled' ? 'failed' : s.state)
const stateColor = (s: S) => statusColor(statusKey(s))
const stateBadge = (s: S) => badgeClass(statusKey(s))

/** Ссылка источника: тип и значение (branch main, tag v1, semver >=1.0); для HelmChart — чарт, для Bucket — имя бакета */
export function sourceRefText(s: Partial<FluxSourceInfo>): string {
  if (s.refType && s.refValue) return `${s.refType}: ${s.refValue}`
  if (s.refValue) return s.refValue
  return chartLabel(s.chart, s.chartVersion) || s.bucketName || ''
}

/** Фабрика определения источника: пять видов отличаются id, видом и plural */
export interface FluxSourceKind {
  id: string
  kind: string
  view: string
  title: string
  plural: string
}

/** Данные приходят одним списком (GetFluxSources): определение оставляет свой вид */
const listKind = async (ns: string[], kind: string): Promise<FluxSourceInfo[]> =>
  ((await callApp('GetFluxSources', ns)) as unknown as FluxSourceInfo[]).filter(s => s.kind === kind)

/** Вкладка «Артефакт»: адрес, ссылка, ревизия и digest (URL без секретов приходит из Go) */
export const SourceArtifactTab = defineComponent({
  name: 'FluxSourceArtifactTab',
  props: { resource: { type: Object, default: () => ({}) } },
  setup(props) {
    return () => {
      const s = props.resource as Partial<FluxSourceInfo>
      const rows: [string, string][] = [
        ['fluxSources.columns.url', s.url ?? ''],
        ['fluxSources.columns.ref', sourceRefText(s)],
        ['defs.columns.revision', s.revision ?? ''],
        ['fluxSources.props.digest', s.digest ?? ''],
        ['fluxSources.props.secretRef', s.secretRef ?? ''],
      ]
      const shown = rows.filter(([, v]) => v)
      if (!shown.length) return h('div', { class: 'rd-empty', 'data-testid': 'flux-artifact-empty' }, t('fluxSources.artifact.empty'))
      return h('div', { 'data-testid': 'flux-artifact' }, shown.map(([k, v]) =>
        h('div', { class: 'rd-row', key: k }, [h('span', { class: 'rd-label' }, t(k)), h('span', { class: 'rd-mono' }, v)])))
    }
  },
})

export function defineFluxSource(k: FluxSourceKind) {
  const yamlKind = () => `cr:${GROUP}/${useFlux().fluxStatus.value.versions?.[k.kind] || 'v1'}/${k.plural}`

  const columns: ColumnDef<FluxSourceInfo>[] = [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: stateLabel, colorFn: stateColor, sortValue: s => fluxStateRank(s.state) },
    { key: 'url', label: 'fluxSources.columns.url', formatter: s => s.url || dash },
    { key: 'ref', label: 'fluxSources.columns.ref', formatter: s => sourceRefText(s) || dash },
    { key: 'revision', label: 'defs.columns.revision', formatter: s => shortRevision(s.revision) || dash },
    { key: 'digest', label: 'fluxSources.props.digest', formatter: s => shortRevision(s.digest) || dash },
    ageCol,
  ]

  return defineResource<FluxSourceInfo>({
    id: k.id,
    kind: k.kind,
    view: k.view,
    title: k.title,
    namespaced: true,
    nav: { group: 'flux-sources', label: k.title },
    editable: false,
    // Источники не удаляются из интерфейса (ADR-0023, раздел 6): ни пачкой, ни по одному
    bulkDelete: false,
    autoRefresh: true,
    refreshOn: () => fluxLiveTick.value,
    columns,
    properties: [
      { label: 'common.status', value: stateLabel, badge: stateBadge },
      { label: 'defs.columns.message', value: s => s.message, hideEmpty: true },
      { label: 'fluxSources.columns.url', value: s => s.url, hideEmpty: true },
      { label: 'fluxSources.columns.ref', value: s => sourceRefText(s), hideEmpty: true },
      { label: 'defs.columns.revision', value: s => s.revision, hideEmpty: true },
      { label: 'fluxSources.props.digest', value: s => s.digest, hideEmpty: true },
      { label: 'fluxSources.props.secretRef', value: s => s.secretRef, hideEmpty: true },
      { label: 'fluxSources.props.interval', value: s => s.interval, hideEmpty: true },
      { label: 'fluxSources.props.suspended', value: s => (s.suspended ? t('common.yes') : null), hideEmpty: true },
      {
        label: 'fluxSources.props.lastReconcile',
        value: s => (s.lastReconcileAt && !Number.isNaN(Date.parse(s.lastReconcileAt)) ? formatDateTime(s.lastReconcileAt) : null),
        hideEmpty: true,
      },
    ],
    tabs: [
      { id: 'fluxConditions', label: 'defs.columns.conditions', component: FluxConditionsSection },
      { id: 'fluxArtifact', label: 'fluxSources.tabs.artifact', component: SourceArtifactTab },
    ],
    actions: [
      {
        id: 'reconcile', label: 'fluxSources.menu.reconcile', icon: 'restart', permission: Perm.Operate,
        run: s => reconcileFlux([s as FluxSourceInfo], { withSource: false }),
      },
      {
        id: 'suspend', label: 'defs.menu.suspend', icon: 'pause', permission: Perm.Write,
        when: s => !s.suspended,
        run: s => setFluxSuspended([s as FluxSourceInfo], true),
      },
      {
        id: 'resume', label: 'defs.menu.resume', icon: 'play', permission: Perm.Write,
        when: s => !!s.suspended,
        run: s => setFluxSuspended([s as FluxSourceInfo], false),
      },
      {
        id: 'editYaml', label: 'fluxSources.menu.editYaml', permission: Perm.Write,
        run: (s, ctx) => ctx.edit(yamlKind(), s.namespace, s.name),
      },
    ],
    api: { list: ns => listKind(ns, k.kind) },
  })
}
