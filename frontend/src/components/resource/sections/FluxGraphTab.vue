<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="mode !== 'unavailable'" class="fg" data-testid="flux-graph">
    <div v-if="error" class="rd-empty" role="alert" data-testid="flux-graph-error">{{ error }}</div>
    <div v-else-if="loading && !graph" class="rd-empty" role="status" data-testid="flux-graph-loading">{{ $t('fluxGraph.loading') }}</div>
    <template v-else-if="graph">
      <div class="fg-toolbar">
        <button type="button" class="rd-btn" :disabled="loading" data-testid="flux-graph-refresh" @click="load">{{ $t('common.refresh') }}</button>
        <span class="fg-legend" aria-hidden="true">
          <span class="fg-leg fg-sourceRef">{{ $t('fluxGraph.legend.sourceRef') }}</span>
          <span class="fg-leg fg-dependsOn">{{ $t('fluxGraph.legend.dependsOn') }}</span>
          <span class="fg-leg fg-inventory">{{ $t('fluxGraph.legend.inventory') }}</span>
          <span class="fg-leg fg-owns">{{ $t('fluxGraph.legend.owns') }}</span>
        </span>
      </div>

      <div v-if="mode === 'remote'" class="fg-note fg-note-warn" data-testid="flux-graph-remote">{{ $t('fluxGraph.remote') }}</div>
      <div v-if="graph.truncated" class="fg-note fg-note-warn" data-testid="flux-graph-truncated">{{ $t('fluxGraph.truncated', { n: graph.nodes.length }) }}</div>
      <div v-for="w in graph.warnings" :key="w" class="fg-note fg-note-warn">{{ errorText(w) }}</div>

      <div v-if="layout.nodes.length" class="fg-scroll">
        <svg :width="layout.width + 2" :height="layout.height + 2" class="fg-svg" role="group" :aria-label="$t('fluxGraph.ariaLabel')">
          <path v-for="e in layout.edges" :key="e.id" :d="e.d" class="fg-edge" :class="'fg-edge-' + e.type" />
          <g
            v-for="n in layout.nodes"
            :key="n.id"
            class="fg-node"
            :class="[`fg-${nodeTone(n)}`, { 'fg-click': isClickable(n), 'fg-current': isCurrent(n) }]"
            :transform="`translate(${n.x},${n.y})`"
            :role="isClickable(n) ? 'button' : 'img'"
            :tabindex="isClickable(n) ? 0 : undefined"
            :aria-label="label(n)"
            :data-testid="`flux-graph-node-${n.role}`"
            :data-state="nodeStateCode(n)"
            @click="onNodeClick(n)"
            @keydown.enter.prevent="onNodeClick(n)"
            @keydown.space.prevent="onNodeClick(n)"
          >
            <title>{{ label(n) }}</title>
            <rect :width="n.w" :height="n.h" rx="6" />
            <text x="10" y="19" class="fg-kind">{{ n.kind }}</text>
            <text x="10" y="35" class="fg-name">{{ title(n) }}</text>
            <!-- подпись состояния рядом с цветом: смысл не держится на одном цвете -->
            <text :x="n.w - 10" y="19" text-anchor="end" class="fg-state">{{ stateText(n) }}</text>
          </g>
        </svg>
      </div>
      <div v-else class="rd-empty" data-testid="flux-graph-empty">{{ $t('fluxGraph.empty') }}</div>

      <div v-if="collapsedGroups.length" class="fg-note" data-testid="flux-graph-groups">
        {{ $t('fluxGraph.collapsedGroups') }}
        <button v-for="g in collapsedGroups" :key="g.id" type="button" class="rd-btn fg-expand" @click="expand(g.id)">
          {{ $t('fluxGraph.group', { n: g.count, kind: g.kind }) }}
        </button>
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { callApp } from '../../../resources/wails'
import { getResourceById } from '../../../resources/registry'
import {
  GROUP_PREFIX, graphMode, layoutFluxGraph, nodeStateCode, nodeTone, type PlacedFluxNode,
} from '../../../resources/fluxGraph'
import { errorText } from '../../../composables/errorText'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import type { FluxGraph, FluxObjectInfo } from '../../../types/flux'

// Цепочка «источник → объект Flux → ресурсы» одного объекта (GetFluxGraph)
const props = defineProps<{ resource: Partial<FluxObjectInfo> }>()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string; item?: any }): void
}>()
const { t } = useI18n()

const graph = ref<FluxGraph | null>(null)
const loading = ref(false)
const error = ref('')
const expandedGroups = ref<Set<string>>(new Set())

/** Номер запроса: ответ по другому объекту или устаревший отбрасывается */
let seq = 0

async function load() {
  const r = props.resource
  if (!r?.kind || !r.name) return
  const my = ++seq
  loading.value = true
  try {
    const g = (await callApp('GetFluxGraph', { kind: r.kind, namespace: r.namespace ?? '', name: r.name })) as unknown as FluxGraph
    if (my !== seq) return
    graph.value = g
    error.value = ''
  } catch (e) {
    if (my !== seq) return
    error.value = errorText(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

// Другой объект: сбросить граф и раскрытые группы. Тот же объект изменился (flux:changed): перечитать
watch(
  () => `${props.resource?.kind}/${props.resource?.namespace}/${props.resource?.name}`,
  () => {
    graph.value = null
    error.value = ''
    expandedGroups.value = new Set()
    void load()
  },
  { immediate: true },
)
watch(fluxLiveTick, () => { void load() })

const mode = computed(() => graphMode(graph.value))
const layout = computed(() => layoutFluxGraph(graph.value, expandedGroups.value))
const collapsedGroups = computed(() => layout.value.nodes.filter(n => n.id.startsWith(GROUP_PREFIX)))

const expand = (id: string) => { expandedGroups.value = new Set(expandedGroups.value).add(id) }

const isGroup = (n: PlacedFluxNode) => n.id.startsWith(GROUP_PREFIX)
const stateText = (n: PlacedFluxNode) => t(`fluxGraph.state.${nodeStateCode(n)}`)
const title = (n: PlacedFluxNode) => {
  const s = isGroup(n) ? t('fluxGraph.group', { n: n.count, kind: n.kind }) : n.name
  return s.length > 26 ? `${s.slice(0, 25)}…` : s
}
const label = (n: PlacedFluxNode) =>
  isGroup(n)
    ? `${t('fluxGraph.group', { n: n.count, kind: n.kind })}: ${stateText(n)}`
    : [`${n.kind} ${n.namespace ? `${n.namespace}/` : ''}${n.name}`, stateText(n)].join(': ')

const isCurrent = (n: PlacedFluxNode) =>
  n.role === 'flux' && n.kind === props.resource?.kind && n.name === props.resource?.name && n.namespace === (props.resource?.namespace ?? '')

/** Открывается только то, что приложение умеет показывать; текущий объект и схлопнутая группа не открываются как ресурс */
function target(n: PlacedFluxNode): { id: string; ns: string; name: string } | null {
  if (isGroup(n) || isCurrent(n) || !n.name) return null
  const def = n.role === 'resource' ? getResourceById(n.kind) : getResourceById(`flux-${n.kind.toLowerCase()}`)
  if (!def) return null
  if (def.namespaced && !n.namespace) return null
  return { id: def.id, ns: def.namespaced ? n.namespace : '', name: n.name }
}
const isClickable = (n: PlacedFluxNode) => isGroup(n) || !!target(n)

function onNodeClick(n: PlacedFluxNode) {
  if (isGroup(n)) return expand(n.id)
  const tgt = target(n)
  if (tgt) emit('open', tgt)
}
</script>

<style scoped>
.fg-toolbar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.fg-legend { margin-left: auto; display: flex; gap: 10px; font-size: 0.75rem; flex-wrap: wrap; }
.fg-leg::before { content: ''; display: inline-block; width: 18px; height: 0; margin-right: 5px; vertical-align: middle; border-top: 2px solid; }
.fg-sourceRef::before { border-color: #8b5cf6; }
.fg-dependsOn::before { border-color: #fbbf24; border-top-style: dashed; }
.fg-inventory::before { border-color: #6b6d8a; }
.fg-owns::before { border-color: #6b6d8a; border-top-style: dotted; }
.fg-note { padding: 6px 0; font-size: 0.8rem; color: #9ca3af; }
.fg-note-warn { color: #facc15; }
.fg-expand { margin-left: 6px; }
.fg-scroll { overflow: auto; max-height: calc(100vh - 340px); min-height: 200px; border: 1px solid #2a2f47; border-radius: 6px; padding: 10px; background: #12141f; }
.fg-svg { display: block; }

.fg-edge { fill: none; stroke: #6b6d8a; stroke-width: 1.4; }
.fg-edge-sourceRef { stroke: #8b5cf6; }
.fg-edge-dependsOn { stroke: #fbbf24; stroke-dasharray: 5 4; }
.fg-edge-owns, .fg-edge-selects, .fg-edge-routes { stroke-dasharray: 2 3; }

.fg-node rect { fill: #1a1d2e; stroke: #3b3d5c; stroke-width: 1.4; }
.fg-ok rect { stroke: #4ade80; }
.fg-warn rect { stroke: #fbbf24; }
.fg-err rect { stroke: #f87171; fill: rgba(248, 113, 113, 0.08); }
.fg-unk rect { stroke: #6b6d8a; }
.fg-current rect { stroke-width: 2.6; }
.fg-click { cursor: pointer; }
.fg-click:hover rect { fill: #20243a; }
.fg-node:focus-visible { outline: none; }
.fg-node:focus-visible rect { stroke: #60a5fa; stroke-width: 2.6; }
.fg-kind { fill: #8b91ad; font-size: 10px; text-transform: uppercase; letter-spacing: 0.03em; }
.fg-name { fill: #e6e8f5; font-size: 12px; font-family: 'Courier New', monospace; }
.fg-state { fill: #c4c7dc; font-size: 9px; font-weight: 600; }
</style>
