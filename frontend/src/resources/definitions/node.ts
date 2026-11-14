// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, ageCol, controlledByProp, managedByProp } from './common'
import NodeUsageCell from '../../components/resource/sections/NodeUsageCell.vue'
import NodeMetricsSection from '../../components/resource/sections/NodeMetricsSection.vue'
import NodeTaintsSection from '../../components/resource/sections/NodeTaintsSection.vue'
import NodeConditionsSection from '../../components/resource/sections/NodeConditionsSection.vue'
import NodeCapacityTab from '../../components/resource/sections/NodeCapacityTab.vue'
import NodeAllocatableTab from '../../components/resource/sections/NodeAllocatableTab.vue'
import NodePodsTab from '../../components/resource/sections/NodePodsTab.vue'
import { Perm } from '../../types/auth'
import { t } from '../../i18n'
import { errorText } from '../../composables/errorText'
import { hasCapability } from '../../composables/useCapabilities'

/** Цвет Conditions: зелёный — Ready без проблем, жёлтый — Ready с pressure, красный — NotReady */
const conditionsColor = (n: any): string => {
  if (n.status !== 'Ready') return 'var(--red)'
  return n.conditions === 'Ready' ? 'var(--green)' : 'var(--yellow)'
}

export const node = defineResource<k8s.NodeInfo, k8s.NodeDetail>({
  id: 'node',
  kind: 'Node',
  view: 'nodes',
  title: 'Nodes',
  namespaced: false,
  nav: { label: 'Nodes' },

  autoRefresh: true,
  icon: 'service',

  columns: [
    flatNameCol,
    { key: 'cpu', label: 'defs.columns.cpu', width: 100, component: NodeUsageCell },
    { key: 'memory', label: 'defs.columns.memory', width: 100, component: NodeUsageCell, sortValue: (n: any) => n.usage?.memory?.pct },
    { key: 'disk', label: 'defs.columns.disk', width: 100, component: NodeUsageCell, sortValue: (n: any) => n.usage?.disk?.pct },
    { key: 'taints', label: 'defs.columns.taints', width: 80 },
    // kubectl показывает <none> для worker, в списке оставляем пусто
    { key: 'roles', label: 'defs.columns.roles', formatter: n => (n.roles === '<none>' ? '' : n.roles) },
    { key: 'version', label: 'defs.columns.version' },
    ageCol,
    { key: 'conditions', label: 'defs.columns.conditions', colorFn: conditionsColor },
  ],

  // Использование CPU / памяти / диска подмешивается к строкам, когда придут метрики
  onLoaded: async items => {
    let list: any[] = []
    try {
      list = ((await callApp('GetNodeMetrics')) as any[]) ?? []
    } catch {
      /* стек метрик не установлен или Prometheus недоступен: список остаётся без полосок */
    }
    if (!list.length) return items
    const byName = new Map(list.map(m => [m.name, m]))
    return items.map(n => {
      const m = byName.get(n.name)
      if (!m) return n
      return {
        ...n,
        usage: {
          cpu: { pct: m.cpuPercent, used: m.cpuUsedCores, total: m.cpuCores },
          memory: { pct: m.memoryPercent, used: m.memoryUsedBytes, total: m.memoryTotalBytes },
          disk: { pct: m.diskPercent, used: m.diskUsedBytes, total: m.diskTotalBytes },
        },
      }
    }) as any[]
  },

  // Панель: Metrics -> Labels -> Annotations -> properties -> sections (Taints, Conditions)
  headSections: [NodeMetricsSection],
  properties: [
    {
      label: 'defs.props.addresses',
      multiline: true,
      value: n => (n.addresses ?? []).map(a => `${a.type}: ${a.address}`).join('\n'),
    },
    { label: 'defs.props.os', value: n => (n.operatingSystem ? `${n.operatingSystem} (${n.architecture})` : n.architecture) },
    { label: 'defs.props.osImage', value: n => n.osImage },
    { label: 'defs.props.kernelVersion', value: n => n.kernelVersion },
    { label: 'defs.props.containerRuntime', value: n => n.containerRuntime },
    { label: 'defs.props.kubeletVersion', value: n => n.kubeVersion },
    controlledByProp,
    managedByProp,
  ],
  sections: [NodeTaintsSection, NodeConditionsSection],
  tabs: [
    { id: 'capacity', label: 'defs.tabs.capacity', component: NodeCapacityTab },
    { id: 'allocatable', label: 'defs.tabs.allocatable', component: NodeAllocatableTab },
    { id: 'pods', label: 'defs.tabs.pods', component: NodePodsTab },
  ],

  actions: [
    {
      id: 'shell',
      label: 'defs.menu.shell',
      icon: 'terminal',
      permission: Perm.NodeShell,
      when: () => hasCapability('nodeShell'),
      run: async (n, ctx) => {
        if (!(await ctx.confirm(
          t('defs.node.shellConfirm', { name: n.name }),
        ))) return
        try {
          const s = await callApp('StartNodeShell', n.name)
          await ctx.openAttach(s.namespace, s.pod, s.container)
        } catch (e: any) {
          ctx.notifyError(t('defs.node.shellError', { error: errorText(e) }))
        }
      },
    },
    {
      id: 'cordon',
      label: 'defs.menu.cordon',
      icon: 'pause',
      permission: Perm.Operate,
      when: n => !n.unschedulable,
      run: (n, ctx) =>
        ctx.perform(() => callApp('CordonNode', n.name, true), t('defs.node.cordoned', { name: n.name })),
    },
    {
      id: 'uncordon',
      label: 'defs.menu.uncordon',
      icon: 'play',
      permission: Perm.Operate,
      when: n => !!n.unschedulable,
      run: (n, ctx) =>
        ctx.perform(() => callApp('CordonNode', n.name, false), t('defs.node.uncordoned', { name: n.name })),
    },
    {
      id: 'drain',
      label: 'defs.menu.drain',
      icon: 'evict',
      permission: Perm.Write,
      run: async (n, ctx) => {
        if (!(await ctx.confirm(
          t('defs.node.drainConfirm', { name: n.name }),
        ))) return
        return ctx.perform(() => callApp('DrainNode', n.name), t('defs.node.drained', { name: n.name }))
      },
    },
  ],

  // Узлы удаляют осознанно и по одному
  bulkDelete: false,

  api: {
    list: () => callApp('GetNodes'),
    get: (_ns, name) => callApp('GetNodeDetail', name),
    remove: (_ns, name) => callApp('DeleteResource', 'node', '', name),
  },
})
