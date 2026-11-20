<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <ul v-if="lines.length" class="metrics" data-testid="metric-facts">
    <li v-for="l in lines" :key="l.id" class="metric" data-testid="metric-fact" :data-signal="l.signal">
      <span class="badge">{{ $t('incidentsIntel.metric.badge') }}</span>
      <span class="line" data-testid="metric-line">{{ l.text }}</span>
    </li>
  </ul>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { isMetricFact, metricLine } from '../../composables/metricFormat'
import type { Fact } from '../../types/incident'

/** Факты metric.*: значение, окно, агрегат и лимит одной строкой (ADR-0032 §6). Не-метрические факты пропускаются. */
const props = defineProps<{ facts?: Fact[] }>()
const lines = computed(() => (props.facts ?? []).filter(isMetricFact).map(metricLine))
</script>

<style scoped>
.metrics { list-style: none; margin: 4px 0 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
.metric { font-size: 12px; color: #a6adc8; }
.badge { margin-right: 6px; padding: 0 6px; border-radius: 4px; background: rgba(148, 226, 213, 0.18); color: #94e2d5; font-size: 11px; font-weight: 600; }
.line { color: #cdd6f4; }
</style>
