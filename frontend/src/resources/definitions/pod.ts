// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { Perm } from '../../types/auth'
import type { k8s as models } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { useK8s } from '../../composables/useK8s'
import { flatNameCol, flatNsCol, ageCol, managedByProp } from './common'
import { badgeClass, statusColor } from '../status'
import { containersOf } from '../utils'
import { podCpuCol, podMemoryCol } from '../podUsage'

import PodContainersCell from '../../components/resource/sections/PodContainersCell.vue'
import PodMetricsSection from '../../components/resource/sections/PodMetricsSection.vue'
import PodVolumesSection from '../../components/resource/sections/PodVolumesSection.vue'
import ContainersTab from '../../components/resource/sections/ContainersTab.vue'
import TolerationsSection from '../../components/resource/sections/TolerationsSection.vue'
import ConditionsSection from '../../components/resource/sections/ConditionsSection.vue'
import { t } from '../../i18n'
import { hasCapability } from '../../composables/useCapabilities'

const k8s = useK8s()

function podStatus(p: any): string {
  return p.status || 'Unknown'
}

/** Маппинг kind -> resource id для ссылок */
const kindToId: Record<string, string> = {
  ReplicaSet: 'replicaset',
  StatefulSet: 'statefulset',
  DaemonSet: 'daemonset',
  Job: 'job',
  CronJob: 'cronjob',
  Deployment: 'deployment',
  Node: 'node',
  Service: 'service',
}

function controlledByLink(p: any): { id: string; ns: string; name: string } | null {
  const cb = p.controlledBy
  if (!cb) return null
  const [kind, name] = cb.split('/')
  if (!kind || !name) return null
  const id = kindToId[kind]
  if (!id) return null
  return { id, ns: p.namespace, name }
}

export const pod = defineResource<models.PodInfo, models.PodDetail>({
  id: 'pod',
  kind: 'Pod',
  view: 'pods',
  title: 'Pods',
  namespaced: true,
  nav: { group: 'workloads', label: 'Pods' },
  autoRefresh: true,

  columns: [
    flatNameCol,
    { ...flatNsCol, link: (p: any) => p.namespace ? { id: 'namespace', ns: '', name: p.namespace } : null },
    podCpuCol,
    podMemoryCol,
    {
      key: 'containers',
      label: 'defs.columns.containers',
      component: PodContainersCell,
    },
    {
      key: 'status',
      label: 'common.status',
      className: 'status-label',
      formatter: podStatus,
      colorFn: p => statusColor(podStatus(p)),
    },
    { key: 'restarts', label: 'defs.columns.restarts', sortValue: (p: any) => Number.parseInt(p.restarts, 10) },
    ageCol,
    { key: 'node', label: 'defs.columns.node', link: (p: any) => p.node ? { id: 'node', ns: '', name: p.node } : null },
    {
      key: 'controlledBy',
      label: 'defs.columns.controller',
      className: 'ns-label',
      formatter: (p: any) => p.controlledBy?.split('/')[0] || '—',
      link: controlledByLink,
    },
    { key: 'qos', label: 'defs.columns.qos' },
  ],

  headSections: [PodMetricsSection],
  properties: [
    { label: 'common.status', value: p => podStatus(p), badge: p => badgeClass(podStatus(p)) },
    { label: 'defs.columns.ready', value: p => p.ready },
    { label: 'defs.columns.restarts', value: p => p.restarts },
    { label: 'defs.columns.node', value: p => p.node, link: p => p.node ? { id: 'node', ns: '', name: p.node } : null },
    { label: 'defs.props.ip', value: p => p.ip },
    {
      label: 'defs.props.controlledBy',
      value: p => {
        const cb = p.controlledBy
        if (!cb) return null
        const [kind] = cb.split('/')
        const hasLink = kind && kindToId[kind]
        return hasLink ? kind + ' ' : cb
      },
      linkLabel: p => {
        const cb = p.controlledBy
        if (!cb) return null
        const [, name] = cb.split('/')
        return name || null
      },
      link: controlledByLink,
    },
    { label: 'defs.props.serviceAccount', value: p => p.serviceAccount, link: p => p.serviceAccount ? { id: 'serviceaccount', ns: p.namespace, name: p.serviceAccount } : null },
    { label: 'defs.columns.qos', value: p => p.qos },
    managedByProp,
  ],
  sections: [PodVolumesSection, TolerationsSection, ConditionsSection],
  tabs: [
    { id: 'containers', label: 'defs.tabs.containers', component: ContainersTab },
  ],

  actions: [
    {
      id: 'attach',
      permission: Perm.Exec,
      when: () => hasCapability('exec'),
      label: 'defs.menu.attach',
      icon: 'attach',
      perContainer: true,
      run: (p, ctx, container) => {
        const c = container ?? containersOf(p)[0]?.name
        if (!c) return ctx.notifyError(t('defs.pod.noContainers'))
        return ctx.openAttach(p.namespace, p.name, c)
      },
    },
    {
      id: 'shell',
      permission: Perm.Exec,
      when: () => hasCapability('exec'),
      label: 'defs.menu.shell',
      icon: 'terminal',
      perContainer: true,
      run: (p, ctx, container) => {
        const c = container ?? containersOf(p)[0]?.name
        if (!c) return ctx.notifyError(t('defs.pod.noContainers'))
        return ctx.openShell(p.namespace, p.name, c)
      },
    },
    {
      id: 'logs',
      permission: Perm.Read,
      label: 'defs.menu.logs',
      icon: 'logs',
      perContainer: true,
      run: (p, ctx, container) => {
        const containers = containersOf(p)
        ctx.openLogs(p.namespace, p.name, containers, container ?? containers[0]?.name)
      },
    },
    {
      id: 'evict',
      label: 'defs.menu.evict',
      icon: 'evict',
      danger: true,
      run: async (p, ctx) => {
        if (!(await ctx.confirm(t('defs.pod.evictConfirm', { name: p.name })))) return
        return ctx.perform(
          () => callApp('EvictPod', p.namespace, p.name),
          t('defs.pod.evicted', { name: p.name }),
        )
      },
    },
  ],

  // Список показывается сразу, метрики подмешиваются, когда придут
  onLoaded: async (items) => {
    await k8s.loadPodMetrics()
    return k8s.mergePodMetrics(items, k8s.podMetrics.value)
  },

  api: {
    list: ns => callApp('GetPods', ns),
    get: (ns, name) => callApp('GetPodDetail', ns, name),
    remove: (ns, name) => callApp('DeleteResource', 'pod', ns, name),
  },
})
