// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../../types'
import { t } from '../../../i18n'
import { formatDateTime } from '../../../i18n/format'
import { callApp } from '../../wails'
import { badgeClass, statusColor } from '../../status'
import { flatNameCol, flatNsCol, ageCol } from '../common'
import { fluxStateRank, sourceRefLabel } from '../../flux'
import { useFlux } from '../../../composables/useFlux'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import { Perm } from '../../../types/auth'
import type { FluxAlertInfo, FluxObjectInfo, FluxRef } from '../../../types/flux'
import FluxConditionsSection from '../../../components/resource/sections/FluxConditionsSection.vue'

const KNOWN = ['Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled']
export const dash = '—'

/** Общие для Alert, Provider и Receiver помощники: подпись и цвет состояния, список ссылок, время */
type S = Pick<FluxObjectInfo, 'state'>
export const notifyStateLabel = (o: S) => t(`flux.state.${KNOWN.includes(o.state) ? o.state : 'Unknown'}`)
const statusKey = (o: S) => (o.state === 'Reconciling' ? 'progressing' : o.state === 'Stalled' ? 'failed' : o.state)
export const notifyStateColor = (o: S) => statusColor(statusKey(o))
export const notifyStateBadge = (o: S) => badgeClass(statusKey(o))
export const notifyStateRank = (o: S) => fluxStateRank(o.state)
/** Ссылки через запятую: Kind/name; пустой список и null — пустая строка */
export const refList = (refs?: FluxRef[] | null) => (refs ?? []).map(sourceRefLabel).filter(Boolean).join(', ')
export const notifyTime = (v?: string) => (v && !Number.isNaN(Date.parse(v)) ? formatDateTime(v) : null)
export const notifyConditionsTab = { id: 'fluxConditions', label: 'defs.columns.conditions', component: FluxConditionsSection }

const yamlKind = () => `cr:notification.toolkit.fluxcd.io/${useFlux().fluxStatus.value.versions?.Alert || 'v1beta3'}/alerts`

/** Alert (Flux CD): только чтение. reconcile, suspend и resume для него не разрешены (ADR-0023, раздел 6) */
export const fluxAlert = defineResource<FluxAlertInfo>({
  id: 'flux-alert',
  kind: 'Alert',
  view: 'flux-alerts',
  title: 'Alerts',
  namespaced: true,
  nav: { group: 'flux-notifications', label: 'Alerts' },
  editable: false,
  bulkDelete: false,
  autoRefresh: true,
  refreshOn: () => fluxLiveTick.value,
  columns: [
    flatNameCol,
    flatNsCol,
    { key: 'state', label: 'common.status', formatter: notifyStateLabel, colorFn: notifyStateColor, sortValue: notifyStateRank },
    { key: 'providerRef', label: 'fluxNotify.columns.provider', formatter: a => a.providerRef || dash },
    { key: 'eventSeverity', label: 'fluxNotify.columns.severity', formatter: a => a.eventSeverity || dash },
    { key: 'eventSources', label: 'fluxNotify.columns.eventSources', formatter: a => refList(a.eventSources) || dash },
    ageCol,
  ],
  properties: [
    { label: 'common.status', value: notifyStateLabel, badge: notifyStateBadge },
    { label: 'defs.columns.message', value: a => a.message, hideEmpty: true },
    { label: 'fluxNotify.columns.provider', value: a => a.providerRef, hideEmpty: true },
    { label: 'fluxNotify.columns.severity', value: a => a.eventSeverity, hideEmpty: true },
    { label: 'fluxNotify.columns.eventSources', value: a => refList(a.eventSources), multiline: true, hideEmpty: true },
    { label: 'fluxNotify.props.lastReconcile', value: a => notifyTime(a.lastReconcileAt), hideEmpty: true },
  ],
  tabs: [notifyConditionsTab],
  // Edit YAML только у Alert: в YAML Provider (spec.address) и Receiver (status.webhookPath) лежат значения, которые нельзя показывать
  actions: [
    {
      id: 'editYaml', label: 'fluxNotify.menu.editYaml', permission: Perm.Write,
      run: (a, ctx) => ctx.edit(yamlKind(), a.namespace, a.name),
    },
  ],
  api: {
    list: async (ns: string[]) => ((await callApp('GetFluxNotifications', ns)).alerts ?? []) as unknown as FluxAlertInfo[],
  },
})
