<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="im-viewer">
    <div class="im-tabs">
      <button
        v-for="tb in tabs"
        :key="tb.id"
        :class="['im-btn', { active: selected === tb.id }]"
        @click="selected = tb.id"
      >{{ tb.label }}</button>
    </div>
    <div class="im-chart">
      <MultiLineChart
        :key="`${ingress}/${selected}`"
        :title="current.title"
        unit=""
        :fetch-data="current.fetch"
        :format-value="current.format"
        :refresh-interval-ms="30000"
      />
    </div>
    <p class="im-hint">{{ $t('metrics.ingressHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import MultiLineChart from './MultiLineChart.vue'
import { usePrometheusRange } from '../../composables/useMetrics'

/**
 * Метрики Ingress (ingress-nginx).
 * Каждый запрос обязан вернуть ОДНУ серию: usePrometheusRange берёт result[0], поэтому везде sum().
 *  - Network: скорость отправки байт для ответов 2xx и 5xx;
 *  - Duration: средняя длительность запроса / ответа (sum / count гистограммы).
 */
const props = defineProps<{ clusterName: string; namespace: string; ingress: string }>()
const { t } = useI18n()
const { queryRange } = usePrometheusRange(props.clusterName)

type Series = { label: string; data: [number, number][] }
interface TabDef {
  id: string
  label: string
  title: string
  format: (v: number) => string
  fetch: (start: number, end: number, step: number) => Promise<Series[]>
}

const esc = (v: string) => v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
const sel = computed(() => `ingress="${esc(props.ingress)}",namespace="${esc(props.namespace)}"`)
const P = 'nginx_ingress_controller_'

const sumRate = (metric: string, extra = '') => `sum(rate(${P}${metric}{${sel.value}${extra}}[5m]))`
// bytes_sent есть не во всех версиях контроллера: запасной вариант — размер ответа
const bytesSent = (status: string) =>
  `${sumRate('bytes_sent_sum', `,status=~"${status}"`)} or ${sumRate('response_size_sum', `,status=~"${status}"`)}`
// Средняя длительность: sum/count; без трафика результат пуст (пропуск на графике), а не NaN
const avgDuration = (name: string) => `${sumRate(`${name}_sum`)} / ${sumRate(`${name}_count`)} > 0`

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
function bytesPerSec(value: number): string {
  if (!isFinite(value) || value <= 0) return '0'
  let v = value
  let i = 0
  while (v >= 1024 && i < BYTE_UNITS.length - 1) { v /= 1024; i++ }
  return `${i ? v.toFixed(1) : Math.round(v)} ${BYTE_UNITS[i]}/s`
}
const seconds = (v: number) => (v < 1 ? `${(v * 1000).toFixed(0)} ms` : `${v.toFixed(2)} s`)

function tab(id: string, label: string, title: string, format: (v: number) => string, lines: [string, string][]): TabDef {
  return {
    id, label, title, format,
    fetch: async (start, end, step) => {
      const data = await Promise.all(lines.map(([, q]) => queryRange(q, start, end, step)))
      return lines.map(([name], i) => ({ label: name, data: data[i] as [number, number][] }))
    },
  }
}

const tabs = computed<TabDef[]>(() => [
  tab('network', t('metrics.tabs.network'), t('metrics.tabs.network'), bytesPerSec, [
    [t('metrics.bytesSent2xx'), bytesSent('2..')],
    [t('metrics.bytesSent5xx'), bytesSent('5..')],
  ]),
  tab('duration', t('metrics.tabs.duration'), t('metrics.tabs.duration'), seconds, [
    [t('metrics.request'), avgDuration('request_duration_seconds')],
    [t('metrics.response'), avgDuration('response_duration_seconds')],
  ]),
])

const selected = ref('network')
const current = computed(() => tabs.value.find(tb => tb.id === selected.value) ?? tabs.value[0])
</script>

<style scoped>
.im-viewer { display: flex; flex-direction: column; gap: 12px; width: 100%; }
.im-tabs { display: flex; gap: 6px; flex-wrap: wrap; }
.im-btn {
  padding: 6px 12px; background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 4px;
  color: var(--text-soft); font-size: 0.85rem; font-weight: 500; cursor: pointer; transition: all 0.15s; white-space: nowrap;
}
.im-btn:hover { background: #262740; border-color: #5a5c7a; color: #c0c2d8; }
.im-btn.active { background: var(--blue-strong); border-color: var(--blue-strong); color: white; }
.im-chart { background: var(--panel); border: 1px solid var(--line); border-radius: 6px; padding: 12px; width: 100%; }
.im-hint { margin: 0; font-size: 0.75rem; color: var(--text-dim); text-align: left; }
</style>