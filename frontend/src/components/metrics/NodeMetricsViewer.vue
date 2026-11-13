<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="nm-viewer">
    <div class="nm-tabs">
      <button
        v-for="tb in tabs"
        :key="tb.id"
        :class="['nm-btn', { active: selected === tb.id }]"
        @click="selected = tb.id"
      >{{ tb.label }}</button>
    </div>

    <div class="nm-charts">
      <div v-for="(c, i) in current.charts" :key="`${nodeName}/${selected}/${i}`" class="nm-chart">
        <MultiLineChart
          :title="c.title"
          unit=""
          :fetch-data="c.fetch"
          :format-value="c.format"
          :refresh-interval-ms="30000"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import MultiLineChart from './MultiLineChart.vue'
import { usePrometheusRange } from '../../composables/useMetrics'

/**
 * Метрики одного узла: CPU / Memory / Network / Disk.
 *  - node_*    (node-exporter) — фактическое использование; узел выбирается меткой kubernetes_node
 *                (см. promNodeLabel в Go);
 *  - kube_*    (kube-state-metrics) — requests и ёмкость; там узел лежит в метке node;
 *  - container_* (cAdvisor) — нагрузка подов; к узлу привязываем через kube_pod_info{node=...},
 *                потому что у cAdvisor-серий нет имени Node.
 * Каждый запрос обязан вернуть ОДНУ серию: usePrometheusRange берёт result[0].
 */
const props = defineProps<{ clusterName: string; nodeName: string }>()
const { t } = useI18n()
const { queryRange } = usePrometheusRange(props.clusterName)

type Series = { label: string; data: [number, number][] }
interface ChartDef {
  title: string
  format: (v: number) => string
  fetch: (start: number, end: number, step: number) => Promise<Series[]>
}
interface TabDef { id: string; label: string; charts: ChartDef[] }

// ─── Форматтеры ───
const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
function bytes(value: number): string {
  if (!isFinite(value) || value <= 0) return '0'
  let v = value
  let i = 0
  while (v >= 1024 && i < BYTE_UNITS.length - 1) { v /= 1024; i++ }
  return `${i ? v.toFixed(1) : Math.round(v)} ${BYTE_UNITS[i]}`
}
const bytesPerSec = (v: number) => `${bytes(v)}/s`
function cores(v: number): string {
  if (v < 0.001) return '0'
  return v < 1 ? `${(v * 1000).toFixed(0)}m` : v.toFixed(2)
}
const perSec = (v: number) => (v < 0.01 ? '0' : v.toFixed(2))

// ─── Селекторы ───
const esc = (v: string) => v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
const nodeSel = computed(() => `kubernetes_node="${esc(props.nodeName)}"`)
const ksmSel = computed(() => `node="${esc(props.nodeName)}"`)

// Виртуальные интерфейсы (veth, мосты, оверлей) дублируют трафик
const PHYSICAL_NICS = 'device!~"lo|veth.*|docker.*|cni.*|flannel.*|cali.*|br-.*"'
const ROOT_FS = 'mountpoint="/",fstype!="tmpfs"'

/** Оставляет серии подов, которые стоят на этом узле (kube_pod_info — по одной серии на под) */
const onNode = (expr: string) => `${expr} * on(namespace,pod) group_left() kube_pod_info{${ksmSel.value}}`
/** Requests считаем только у живых подов: завершённые Job'ы ресурсы уже не занимают */
const activePods = (expr: string) =>
  `${expr} * on(namespace,pod) group_left() (kube_pod_status_phase{phase=~"Pending|Running"} == 1)`

function chart(title: string, format: (v: number) => string, lines: [string, string][]): ChartDef {
  return {
    title,
    format,
    fetch: async (start, end, step) => {
      const data = await Promise.all(lines.map(([, q]) => queryRange(q, start, end, step)))
      return lines.map(([label], i) => ({ label, data: data[i] as [number, number][] }))
    },
  }
}

const tabs = computed<TabDef[]>(() => {
  const n = nodeSel.value
  const k = ksmSel.value
  return [
    {
      id: 'cpu',
      label: t('metrics.tabs.cpu'),
      charts: [
        chart(t('metrics.cpu'), cores, [
          [t('metrics.cpuUsage'), `sum(rate(node_cpu_seconds_total{mode!="idle",${n}}[5m]))`],
          [t('metrics.cpuRequests'), `sum(${activePods(`kube_pod_container_resource_requests{resource="cpu",${k}}`)})`],
          [t('metrics.cpuAllocatable'), `sum(kube_node_status_allocatable{resource="cpu",${k}})`],
          [t('metrics.cpuCapacity'), `sum(kube_node_status_capacity{resource="cpu",${k}})`],
        ]),
      ],
    },
    {
      id: 'memory',
      label: t('metrics.tabs.memory'),
      charts: [
        chart(t('metrics.memory'), bytes, [
          [t('metrics.memoryUsage'), `sum(node_memory_MemTotal_bytes{${n}}) - sum(node_memory_MemAvailable_bytes{${n}})`],
          [
            t('metrics.workloadMemoryUsage'),
            `sum(${onNode('container_memory_working_set_bytes{container!="",container!="POD"}')})`,
          ],
          [t('metrics.memoryRequests'), `sum(${activePods(`kube_pod_container_resource_requests{resource="memory",${k}}`)})`],
          [t('metrics.memoryAllocatable'), `sum(kube_node_status_allocatable{resource="memory",${k}})`],
          [t('metrics.memoryCapacity'), `sum(kube_node_status_capacity{resource="memory",${k}})`],
        ]),
      ],
    },
    {
      id: 'network',
      label: t('metrics.tabs.network'),
      charts: [
        chart(t('metrics.networkTraffic'), bytesPerSec, [
          [t('metrics.receive'), `sum(rate(node_network_receive_bytes_total{${n},${PHYSICAL_NICS}}[5m]))`],
          [t('metrics.transmit'), `sum(rate(node_network_transmit_bytes_total{${n},${PHYSICAL_NICS}}[5m]))`],
        ]),
        chart(t('metrics.packetErrors'), perSec, [
          [t('metrics.receiveErrors'), `sum(rate(node_network_receive_errs_total{${n},${PHYSICAL_NICS}}[5m]))`],
          [t('metrics.transmitErrors'), `sum(rate(node_network_transmit_errs_total{${n},${PHYSICAL_NICS}}[5m]))`],
          [t('metrics.receiveDrops'), `sum(rate(node_network_receive_drop_total{${n},${PHYSICAL_NICS}}[5m]))`],
          [t('metrics.transmitDrops'), `sum(rate(node_network_transmit_drop_total{${n},${PHYSICAL_NICS}}[5m]))`],
        ]),
      ],
    },
    {
      id: 'disk',
      label: t('metrics.tabs.disk'),
      charts: [
        chart(t('metrics.disk'), bytes, [
          [
            t('metrics.diskUsage'),
            `max(node_filesystem_size_bytes{${n},${ROOT_FS}}) - max(node_filesystem_avail_bytes{${n},${ROOT_FS}})`,
          ],
          [t('metrics.diskSize'), `max(node_filesystem_size_bytes{${n},${ROOT_FS}})`],
        ]),
      ],
    },
  ]
})

const selected = ref('cpu')
const current = computed(() => tabs.value.find(tb => tb.id === selected.value) ?? tabs.value[0])
</script>

<style scoped>
.nm-viewer { display: flex; flex-direction: column; gap: 12px; width: 100%; }
.nm-tabs { display: flex; gap: 6px; flex-wrap: wrap; }
.nm-btn {
  padding: 6px 12px; background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 4px;
  color: var(--text-soft); font-size: 0.85rem; font-weight: 500; cursor: pointer; transition: all 0.15s; white-space: nowrap;
}
.nm-btn:hover { background: #262740; border-color: #5a5c7a; color: #c0c2d8; }
.nm-btn.active { background: var(--blue-strong); border-color: var(--blue-strong); color: white; }
.nm-charts { display: flex; flex-direction: column; gap: 12px; }
.nm-chart { background: var(--panel); border: 1px solid var(--line); border-radius: 6px; padding: 12px; width: 100%; }
</style>
