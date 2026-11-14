<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="pvc-metrics">
    <MultiLineChart
      :title="$t('metrics.volumeUsage')"
      unit=""
      :fetch-data="fetchData"
      :format-value="formatSize"
      :refresh-interval-ms="30000"
    />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import MultiLineChart from './MultiLineChart.vue'
import { usePrometheusRange } from '../../composables/useMetrics'

const { t } = useI18n()
const props = defineProps<{ clusterName: string; namespace: string; claim: string }>()
const { queryRange } = usePrometheusRange(props.clusterName)

const GIB = 1024 ** 3
const esc = (v: string) => v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
const selector = `namespace="${esc(props.namespace)}",persistentvolumeclaim="${esc(props.claim)}"`

// Метрики отдаёт kubelet и только для смонтированных томов
const q = (metric: string) => `sum(${metric}{${selector}}) / ${GIB}`

async function fetchData(start: number, end: number, step: number) {
  const [used, capacity, available] = await Promise.all([
    queryRange(q('kubelet_volume_stats_used_bytes'), start, end, step),
    queryRange(q('kubelet_volume_stats_capacity_bytes'), start, end, step),
    queryRange(q('kubelet_volume_stats_available_bytes'), start, end, step),
  ])
  return [
    { label: t('metrics.diskUsage'), data: used },
    { label: t('metrics.diskCapacity'), data: capacity },
    { label: t('metrics.diskAvailable'), data: available },
  ]
}

function formatSize(gib: number): string {
  return gib < 1 ? `${(gib * 1024).toFixed(0)} MiB` : `${gib.toFixed(2)} GiB`
}
</script>

<style scoped>
.pvc-metrics { background: var(--panel); border: 1px solid var(--line); border-radius: 6px; padding: 12px; }
</style>