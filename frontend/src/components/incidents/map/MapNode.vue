<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <g class="node" :class="[`h-${node.health}`, `v-${node.variant}`, { selected, chain, dim, candidate: node.candidate, target: node.target }]"
    role="button" :tabindex="focusable ? 0 : -1" :aria-pressed="selected" :aria-label="aria" :transform="`translate(${x} ${y})`"
    data-testid="map-node" :data-key="node.key" :data-health="node.health" :data-variant="node.variant"
    :data-candidate="node.candidate ? 'true' : undefined" :data-chain="chain ? 'true' : undefined"
    @click.stop="emit('select', node.key)" @focus="emit('focus', node.key)">
    <title>{{ fullText }}</title>
    <!-- Форма зависит от вида вершины: заглушка — пунктир, Unknown — точки, Secret — «срезанные углы», группа — стопка -->
    <template v-if="node.variant === 'group'">
      <rect class="stack" :x="6" :y="-6" :width="NODE_W" :height="NODE_H" rx="8" />
      <rect class="stack" :x="3" :y="-3" :width="NODE_W" :height="NODE_H" rx="8" />
    </template>
    <polygon v-if="node.variant === 'secret'" class="body" :points="octagon" />
    <rect v-else class="body" :width="NODE_W" :height="NODE_H" rx="8" />
    <rect v-if="node.candidate" class="ring" :x="-4" :y="-4" :width="NODE_W + 8" :height="NODE_H + 8" rx="11" />
    <text class="kind" x="10" y="17">{{ kindLabel }}</text>
    <text v-if="badge" class="badge" :x="NODE_W - 10" y="17" text-anchor="end">{{ badge }}</text>
    <text class="name" x="10" y="35">{{ node.variant === 'group' ? $t('incidentsMap.group.label', { count: node.members?.length ?? 0, kind: node.kind }) : shortName(node.name) }}</text>
    <text class="state" x="10" y="51"><tspan class="icon">{{ icon }}</tspan> {{ stateLabel }}</text>
  </g>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { HEALTH_ICONS, VARIANT_ICONS, shortName, type MapNode } from '../../../composables/dependencyMap'
import { NODE_H, NODE_W } from '../../../composables/dependencyMapLayout'

const props = defineProps<{
  node: MapNode
  x: number
  y: number
  selected?: boolean
  /** Вершина лежит на подсвеченной цепочке до кандидата */
  chain?: boolean
  /** Вершина вне подсвеченной цепочки (приглушена) */
  dim?: boolean
  /** Единственная вершина в порядке табуляции (roving tabindex) */
  focusable?: boolean
}>()
const emit = defineEmits<{ (e: 'select', key: string): void; (e: 'focus', key: string): void }>()
const { t } = useI18n()

const octagon = `14,0 ${NODE_W - 14},0 ${NODE_W},14 ${NODE_W},${NODE_H - 14} ${NODE_W - 14},${NODE_H} 14,${NODE_H} 0,${NODE_H - 14} 0,14`
const icon = computed(() => VARIANT_ICONS[props.node.variant] || HEALTH_ICONS[props.node.health])
// Вид ресурса — идентификатор Kubernetes (не переводится); состояние и особый вариант — словами.
const kindLabel = computed(() => shortName(props.node.kind, 22))
const stateLabel = computed(() => {
  const v = props.node.variant
  if (v === 'secret' || v === 'placeholder' || v === 'unknown' || v === 'group') return t(`incidentsMap.variant.${v}`)
  return t(`incidentsMap.health.${props.node.health}`)
})
const badge = computed(() => (props.node.candidate ? t('incidentsMap.candidate') : props.node.target ? t('incidentsMap.target') : ''))
const fullText = computed(() => `${props.node.kind}${props.node.name ? `/${props.node.name}` : ''}${props.node.namespace ? ` (${props.node.namespace})` : ''}`)
const aria = computed(() =>
  [props.node.variant === 'group' ? t('incidentsMap.group.label', { count: props.node.members?.length ?? 0, kind: props.node.kind }) : fullText.value, stateLabel.value, badge.value]
    .filter(Boolean).join(', '),
)
</script>

<style scoped>
.node { cursor: pointer; outline: none; }
.body { fill: var(--bg-sunken, #181825); stroke: var(--line-strong, #45475a); stroke-width: 1.5; }
.stack { fill: var(--bg-sunken, #181825); stroke: var(--line-strong, #45475a); stroke-width: 1; }
.kind { font-size: 11px; fill: var(--text-muted, #a6adc8); }
.name { font-size: 13px; font-weight: 600; fill: var(--text, #cdd6f4); }
.state { font-size: 11px; fill: var(--text-muted, #a6adc8); }
.state .icon { font-weight: 700; }
.badge { font-size: 10px; font-weight: 700; fill: var(--warn, #f9e2af); }
.ring { fill: none; stroke: var(--warn, #f9e2af); stroke-width: 2.5; }
.h-Healthy .body { stroke: var(--ok, #a6e3a1); }
.h-Degraded .body { stroke: var(--amber, #fab387); stroke-width: 2.5; }
.h-Progressing .body, .h-Suspended .body { stroke: var(--accent, #89b4fa); }
.h-Missing .body { stroke: var(--danger, #f38ba8); stroke-dasharray: 7 4; fill: rgba(var(--danger-rgb, 243, 139, 168), 0.1); }
.h-Unknown .body, .h-other .body { stroke: var(--text-faint, #9399b2); stroke-dasharray: 2 4; stroke-linecap: round; }
.v-secret .body { stroke-dasharray: 2 4; }
.target .body { stroke-width: 3; }
.selected .body { stroke-width: 3.5; filter: drop-shadow(0 0 4px var(--accent, #89b4fa)); }
.dim { opacity: 0.45; }
.node:focus-visible .body { stroke: var(--accent, #89b4fa); stroke-width: 4; stroke-dasharray: none; }
@media (prefers-reduced-motion: no-preference) {
  .node { transition: opacity 0.15s; }
}
</style>
