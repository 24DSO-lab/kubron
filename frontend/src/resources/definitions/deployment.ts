// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { Perm } from '../../types/auth'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, flatNsCol, ageCol, controlledByProp, managedByProp } from './common'
import { badgeClass, statusColor } from '../status'
import { workloadMetrics } from '../workloadMetrics'

import ImagesSection from '../../components/resource/sections/ImagesSection.vue'
import ConditionsSection from '../../components/resource/sections/ConditionsSection.vue'
import PodAntiAffinitySection from '../../components/resource/sections/PodAntiAffinitySection.vue'
import PodsTab from '../../components/resource/sections/PodsTab.vue'
import RevisionsTab from '../../components/resource/sections/RevisionsTab.vue'
import { t } from '../../i18n'

/** Статус по conditions (Available + Progressing) */
export function deploymentStatus(dep: any): string {
  const conds: any[] = dep?.conditions ?? []
  if (conds.length === 0) return 'Pending'
  const available = conds.find(c => c.type === 'Available')
  const progressing = conds.find(c => c.type === 'Progressing')

  if (available?.status === 'True' && progressing?.status === 'True') return 'Running'
  if (progressing?.status === 'False') return progressing.reason || 'Failed'
  if (progressing?.reason) return progressing.reason
  return 'Pending'
}

function replicasSummary(d: any): string {
  const desired = d.replicas ?? 0
  const ready = d.readyReplicas ?? 0
  return t('defs.deployment.replicasSummary', { desired, updated: d.updatedReplicas ?? 0, total: desired, available: ready, unavailable: Math.max(0, desired - ready) })
}

export const deployment = defineResource<k8s.DeploymentInfo>({
  id: 'deployment',
  kind: 'Deployment',
  view: 'deployments',
  title: 'Deployments',
  namespaced: true,
  nav: { group: 'workloads', label: 'Deployments' },

  autoRefresh: true,
  columns: [
    flatNameCol,
    { ...flatNsCol, link: (d: any) => d.namespace ? { id: 'namespace', ns: '', name: d.namespace } : null },
    {
      key: 'pods',
      label: 'defs.columns.pods',
      formatter: d => `${d.readyReplicas}/${d.replicas}`,
      colorFn: d => (d.readyReplicas === d.replicas ? 'var(--green)' : 'var(--yellow)'),
    },
    { key: 'updatedReplicas', label: 'defs.columns.replicas' },
    ageCol,
    {
      key: 'status',
      label: 'common.status',
      formatter: deploymentStatus,
      colorFn: d => statusColor(deploymentStatus(d)),
    },
  ],

  headSections: [workloadMetrics.deployment],
  properties: [
    { label: 'defs.columns.replicas', value: replicasSummary },
    { label: 'defs.columns.selector', value: d => d.selector },
    { label: 'defs.columns.nodeSelector', value: d => d.nodeSelector },
    { label: 'defs.props.strategyType', value: d => d.strategy },
    { label: 'common.status', value: deploymentStatus, badge: d => badgeClass(deploymentStatus(d)) },
    controlledByProp,
    managedByProp,
  ],
  sections: [ImagesSection, ConditionsSection, PodAntiAffinitySection],
  tabs: [
    { id: 'revisions', label: 'defs.tabs.revisions', component: RevisionsTab },
    { id: 'pods', label: 'defs.tabs.pods', component: PodsTab },
  ],

  actions: [
    { id: 'scale', permission: Perm.Operate, label: 'defs.menu.scale', icon: 'scale', run: (d, ctx) => ctx.requestScale(d, 'Deployment') },
    {
      id: 'restart',
      permission: Perm.Operate,
      label: 'defs.menu.restart',
      icon: 'restart',
      run: async (d, ctx) => {
        if (!(await ctx.confirm(t('defs.deployment.restartConfirm', { name: d.name })))) return
        return ctx.perform(
          () => callApp('RestartDeployment', d.namespace, d.name),
          t('defs.deployment.restarted', { name: d.name }),
        )
      },
    },
    // Название "View logs", а не "Logs": список особо обрабатывает пункт с меткой "Logs" (подменю контейнеров pod)
    { id: 'logs', label: 'defs.menu.viewLogs', icon: 'logs', permission: Perm.Read, run: (d, ctx) => ctx.openResourceLogs('deployment', d.namespace, d.name) },
  ],

  api: {
    list: ns => callApp('GetDeployments', ns),
    remove: (ns, n) => callApp('DeleteResource', 'deployment', ns, n),
  },
})