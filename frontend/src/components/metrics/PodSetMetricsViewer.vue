<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ps-viewer">
    <div class="metrics-selector">
      <button
        v-for="m in list"
        :key="m.id"
        :class="['metric-btn', { active: selected === m.id }]"
        @click="selected = m.id"
      >{{ m.label() }}</button>
    </div>
    <div class="metrics-chart-container">
      <MultiLineChart
        :key="selected"
        :title="cfg.title()"
        :unit="cfg.unit()"
        :fetch-data="fetchData"
        :format-value="cfg.format"
        :refresh-interval-ms="30000"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import MultiLineChart from './MultiLineChart.vue'
import { usePrometheusRange } from '../../composables/useMetrics'

/**
 * Метрики набора подов.
 *  - с ownerKind/ownerRegex: поды одного владельца (Deployment, DaemonSet, ...), выбираются через
 *    kube_pod_owner (kube-state-metrics), а не по имени пода: имя пода обрезается Kubernetes
 *    для длинных имён ресурсов, владелец указан всегда;
 *  - без них: все поды namespace.
 */
const props = defineProps<{
  clusterName: string
  namespace: string
  /** kube_pod_owner.owner_kind: ReplicaSet | DaemonSet | StatefulSet | Job */
  ownerKind?: string
  /** regex для owner_name (в Prometheus он всегда с якорями) */
  ownerRegex?: string
  /** несколько владельцев сразу (приложение из нескольких workload-ов); объединяются с ownerKind/ownerRegex */
  owners?: { kind: string; regex: string }[]
}>()

const { t } = useI18n()
const { queryRange } = usePrometheusRange(props.clusterName)

const ns = () => `namespace="${props.namespace}"`
const ownerSelector = (kind: string, regex: string) =>
  `kube_pod_owner{${ns()},owner_kind="${kind}",owner_name=~"${regex}",owner_is_controller="true"}`
/** Все владельцы одним выражением: серии kube_pod_owner объединяются через `or` */
const owner = () => {
  const list = [...(props.ownerKind ? [{ kind: props.ownerKind, regex: props.ownerRegex ?? '' }] : []), ...(props.owners ?? [])]
  return list.length ? `(${list.map(o => ownerSelector(o.kind, o.regex)).join(' or ')})` : ''
}
/** Оставляет только серии подов нужного владельца (значение kube_pod_owner всегда 1); без владельца — как есть */
const own = (expr: string) => {
  const o = owner()
  return o ? `${expr} * on(namespace,pod) group_left() ${o}` : expr
}

  
// label/title/unit — функции: текст считается при рендере и следует за языком интерфейса
interface Line { label: () => string; expr: () => string }
interface Cfg {
  id: string
  label: () => string
  title: () => string
  unit: () => string
  format: (v: number) => string
  lines: Line[]
}

function formatCpu(v: number): string {
  if (v < 0.001) return '0'
  if (v < 1) return (v * 1000).toFixed(0) + 'm'
  return v.toFixed(2)
}
function formatMemoryMB(v: number): string {
  if (v < 1) return (v * 1024).toFixed(0) + ' KB'
  if (v >= 1024) return (v / 1024).toFixed(2) + ' GB'
  return v.toFixed(1) + ' MB'
}
function formatNetwork(v: number): string {
  if (v < 0.01) return '0.0'
  if (v < 1) return v.toFixed(2)
  if (v < 1000) return v.toFixed(1)
  if (v < 1000000) return (v / 1000).toFixed(1) + 'M'
  return (v / 1000000).toFixed(1) + 'G'
}
const formatDisk = (v: number) => v.toFixed(2)

const list: Cfg[] = [
  {
    id: 'cpu', label: () => t('metrics.tabs.cpu'), title: () => t('metrics.cpuUsage'), unit: () => t('metrics.units.cores'), format: formatCpu,
    lines: [
      {
        label: () => t('metrics.cpuUsage'),
        expr: () => `sum(${own(`rate(container_cpu_usage_seconds_total{${ns()},container!="",container!="POD"}[5m])`)})`,
      },
      {
        label: () => t('metrics.cpuRequests'),
        expr: () => `sum(${own(`kube_pod_container_resource_requests{${ns()},container!="POD",resource="cpu"}`)})`,
      },
      {
        label: () => t('metrics.cpuLimits'),
        expr: () => `sum(${own(`kube_pod_container_resource_limits{${ns()},container!="POD",resource="cpu"}`)})`,
      },
    ],
  },
  {
    id: 'memory', label: () => t('metrics.tabs.memory'), title: () => t('metrics.memoryUsage'), unit: () => 'MB', format: formatMemoryMB,
    lines: [
      {
        label: () => t('metrics.memoryUsage'),
        expr: () => `sum(${own(`container_memory_working_set_bytes{${ns()},container!="",container!="POD"}`)}) / 1024 / 1024`,
      },
      {
        label: () => t('metrics.memoryRequests'),
        expr: () => `sum(${own(`kube_pod_container_resource_requests{${ns()},container!="POD",resource="memory"}`)}) / 1024 / 1024`,
      },
      {
        label: () => t('metrics.memoryLimits'),
        expr: () => `sum(${own(`kube_pod_container_resource_limits{${ns()},container!="POD",resource="memory"}`)}) / 1024 / 1024`,
      },
    ],
  },
  {
    id: 'network', label: () => t('metrics.tabs.network'), title: () => t('metrics.networkTraffic'), unit: () => 'KB/s', format: formatNetwork,
    lines: [
      {
        label: () => t('metrics.receive'),
        expr: () => `sum(${own(`rate(container_network_receive_bytes_total{${ns()}}[5m])`)}) / 1024`,
      },
      {
        label: () => t('metrics.transmit'),
        expr: () => `sum(${own(`rate(container_network_transmit_bytes_total{${ns()}}[5m])`)}) / 1024`,
      },
    ],
  },
  {
    id: 'disk', label: () => t('metrics.tabs.diskIo'), title: () => t('metrics.diskIo'), unit: () => 'MB/s', format: formatDisk,
    lines: [
      {
        label: () => t('metrics.fsUsage'),
        expr: () => `sum(${own(`container_fs_usage_bytes{${ns()}}`)}) / 1024 / 1024`,
      },
      {
        label: () => t('metrics.fsWrites'),
        expr: () => `sum(${own(`rate(container_fs_writes_bytes_total{${ns()}}[5m])`)}) / 1024 / 1024`,
      },
      {
        label: () => t('metrics.fsReads'),
        expr: () => `sum(${own(`rate(container_fs_reads_bytes_total{${ns()}}[5m])`)}) / 1024 / 1024`,
      },
    ],
  },
]

const selected = ref('cpu')
const cfg = computed(() => list.find(m => m.id === selected.value)!)

const fetchData = computed(() => {
  const lines = cfg.value.lines
  return async (start: number, end: number, step: number) => {
    const data = await Promise.all(lines.map(l => queryRange(l.expr(), start, end, step)))
    return lines.map((l, i) => ({ label: l.label(), data: data[i] }))
  }
})
</script>

<style scoped>
.ps-viewer { display: flex; flex-direction: column; gap: 12px; margin-top: 8px; width: 100%; }
.metrics-selector { display: flex; gap: 6px; flex-wrap: wrap; }
.metric-btn {
  padding: 6px 12px; background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 4px;
  color: var(--text-soft); font-size: 0.85rem; font-weight: 500; cursor: pointer; transition: all 0.15s; white-space: nowrap;
}
.metric-btn:hover { background: #262740; border-color: #5a5c7a; color: #c0c2d8; }
.metric-btn.active { background: var(--blue-strong); border-color: var(--blue-strong); color: white; }
.metrics-chart-container {
  background: var(--panel); border: 1px solid var(--line); border-radius: 6px;
  padding: 12px; width: 100%; min-height: 350px;
}
</style>