// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { t } from '../../i18n'
import { errorText } from '../../composables/errorText'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { helmLiveTick, startHelmLive } from '../../composables/useHelmLive'
import { runHelmOp } from '../../composables/useHelmOps'
import { flatNameCol, flatNsCol } from './common'
import { badgeClass, statusColor } from '../status'
import { helmContentTab } from '../helmTabs'
import HelmResourcesTab from '../../components/resource/sections/HelmResourcesTab.vue'
import HelmHistoryTab from '../../components/resource/sections/HelmHistoryTab.vue'
import { Perm } from '../../types/auth'
import HelmUpgradeTab from '../../components/resource/sections/HelmUpgradeTab.vue'
import HelmChartUpgradeTab from '../../components/resource/sections/HelmChartUpgradeTab.vue'
import { helmFluxOwner, withFluxWarning } from '../../components/resource/sections/HelmFluxGuard'

export const helmRelease = defineResource<k8s.HelmReleaseInfo, k8s.HelmReleaseDetail>({
  id: 'helmrelease',
  kind: 'Helm Release',
  view: 'helm-releases',
  title: 'Helm Releases',
  namespaced: false,
  nav: { group: 'helm', label: 'Releases' },

  autoRefresh: true,
  // helm-релиз — агрегат из secret-ов, не отдельный k8s-ресурс: без YAML и удаления
  // (uninstall появится на этапе H2 как отдельное действие)
  editable: false,

  columns: [
    flatNameCol,
    { ...flatNsCol, link: (h: any) => h.namespace ? { id: 'namespace', ns: '', name: h.namespace } : null },
    { key: 'chartVersion', label: 'defs.columns.chartVersion' },
    { key: 'appVersion', label: 'defs.columns.appVersion' },
    { key: 'revision', label: 'defs.columns.revision' },
    { key: 'status', label: 'common.status', colorFn: r => statusColor(r.status) },
    {
      // Релиз под управлением Flux: ручные действия разойдутся с Git (ADR-0023, раздел 9)
      key: 'fluxManagedBy',
      label: 'helm.flux.managedBy',
      formatter: r => (helmFluxOwner(r) ? t('helm.flux.badge') : ''),
      sortValue: r => (helmFluxOwner(r) ? 1 : 0),
    },
    {
      key: 'latestVersion',
      label: 'defs.columns.update',
      formatter: r => (r.latestVersion ? `${r.latestVersion} (${r.latestRepo})` : ''),
      colorFn: () => 'var(--yellow)',
      sortValue: r => r.latestVersion || null,
    },
    { key: 'updated', label: 'defs.columns.updated' },
  ],

  properties: [
    { label: 'common.status', value: r => r.status, badge: r => badgeClass(r.status) },
    { label: 'defs.props.chart', value: r => r.chart },
    { label: 'defs.columns.chartVersion', value: r => r.chartVersion },
    { label: 'defs.props.storage', value: r => (r.storage === 'configmap' ? 'ConfigMap' : 'Secret') },
    {
      label: 'helm.flux.managedBy',
      value: () => null,
      hideEmpty: true,
      link: r => { const o = helmFluxOwner(r); return o ? { id: 'flux-helmrelease', ns: o.namespace, name: o.name } : null },
      linkLabel: r => { const o = helmFluxOwner(r); return o ? t('helm.flux.managedByValue', { namespace: o.namespace, name: o.name }) : null },
    },
    { label: 'defs.props.updateAvailable', value: r => (r.latestVersion ? `${r.latestVersion} (${r.latestRepo})` : '—') },
    { label: 'defs.columns.appVersion', value: r => r.appVersion },
    { label: 'defs.columns.revision', value: r => r.revision },
    { label: 'defs.columns.updated', value: r => r.updated },
    { label: 'defs.props.description', value: r => r.description },
  ],

  tabs: [
    { id: 'helmValues', label: 'defs.tabs.values', component: helmContentTab('values') },
    { id: 'helmNotes', label: 'defs.tabs.notes', component: helmContentTab('notes') },
    { id: 'helmManifest', label: 'defs.tabs.manifest', component: helmContentTab('manifest') },
    { id: 'helmEdit', label: 'defs.tabs.editValues', component: HelmUpgradeTab },
    { id: 'helmUpgradeChart', label: 'defs.tabs.upgradeChart', component: HelmChartUpgradeTab },
    { id: 'helmResources', label: 'defs.tabs.resources', component: HelmResourcesTab },
    { id: 'helmHistory', label: 'defs.tabs.history', component: HelmHistoryTab },

  ],
actions: [
    {
      id: 'uninstall',
      label: 'defs.menu.uninstall',
      icon: 'delete',
      danger: true,
      permission: Perm.Write,
      run: async (r, ctx) => {
        if (!(await ctx.confirm(withFluxWarning(
          'uninstall',
          r,
          r.name,
          t('defs.helmRelease.uninstallConfirm', { namespace: r.namespace, name: r.name }),
        )))) return
        try {
          await runHelmOp({ op: 'uninstall', namespace: r.namespace, release: r.name }, id =>
            callApp('UninstallHelmRelease', r.namespace, r.name, false, id))
          ctx.notifySuccess(t('defs.helmRelease.uninstalled', { name: r.name }))
          ctx.closeDetail()
          await ctx.reload()
        } catch (e: any) {
          ctx.notifyError(t('defs.helmRelease.uninstallError', { message: errorText(e) }))
        }
      },
    },
  ],
  // helm:changed (informer): список и открытая панель обновляются сразу, без ожидания таймера
  refreshOn: () => { startHelmLive(); return helmLiveTick.value },
  liveDetail: true,
  api: {
    list: () => callApp('GetHelmReleases'),
    // history и resources приходят отсюда и дополняют элемент списка
    get: (ns, name) => callApp('GetHelmReleaseDetail', ns, name),
  },
})