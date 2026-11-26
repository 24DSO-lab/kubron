<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <g class="edge" :class="[`s-${edge.state}`, { chain, dim }]" data-testid="map-edge" :data-state="edge.state" :data-type="edge.type"
    :data-chain="chain ? 'true' : undefined" aria-hidden="true">
    <path class="line" :d="d" :marker-end="`url(#${markerPrefix}-${markerState})`" />
    <text v-if="label" class="label" :x="mx" :y="my - 4" text-anchor="middle">{{ label }}</text>
  </g>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import type { MapEdge } from '../../../composables/dependencyMap'

const props = defineProps<{
  edge: MapEdge
  d: string
  mx: number
  my: number
  /** Префикс id маркеров стрелок (уникален для экземпляра карты) */
  markerPrefix: string
  chain?: boolean
  dim?: boolean
  /** Подпись типа связи; пустая — не рисуется (экономит место на больших графах) */
  label?: string
}>()
// Стрелка по состоянию: ок / оборвана / нет цели / не проверена.
const markerState = computed(() => (['ok', 'broken', 'missing', 'unknown'].includes(props.edge.state) ? props.edge.state : 'unknown'))
</script>

<style scoped>
.line { fill: none; stroke: var(--text-faint, #7f849c); stroke-width: 1.5; }
.s-ok .line { stroke: var(--text-faint, #7f849c); }
.s-broken .line { stroke: var(--amber, #fab387); stroke-width: 2.5; }
.s-missing .line { stroke: var(--danger, #f38ba8); stroke-dasharray: 7 4; }
.s-unknown .line, .s-other .line { stroke: var(--text-faint, #9399b2); stroke-dasharray: 2 4; stroke-linecap: round; }
.chain .line { stroke-width: 3; }
.chain.s-ok .line { stroke: var(--warn, #f9e2af); }
.dim { opacity: 0.35; }
.label { font-size: 10px; fill: var(--text-muted, #a6adc8); paint-order: stroke; stroke: var(--bg, #1e1e2e); stroke-width: 3px; }
</style>
