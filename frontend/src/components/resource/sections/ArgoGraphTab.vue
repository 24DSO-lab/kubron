<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ag">
    <div class="ag-toolbar">
      <select v-model="kindFilter" class="ag-select" :title="$t('defs.columns.kind')">
        <option value="">{{ $t('argo.graph.allKinds') }}</option>
        <option v-for="k in kinds" :key="k" :value="k">{{ k }}</option>
      </select>
      <label class="ag-check"><input v-model="onlyProblems" type="checkbox" /> {{ $t('argo.graph.onlyProblems') }}</label>
      <button class="rd-btn" :disabled="loading" @click="load">{{ $t('common.refresh') }}</button>
      <button
        class="rd-btn"
        :title="expanded ? $t('panels.detail.restoreWidth') : $t('argo.graph.expandTitle')"
        :aria-pressed="expanded"
        @click="toggleExpanded"
      >{{ expanded ? $t('argo.graph.collapse') : $t('argo.graph.expand') }}</button>
      <span class="ag-legend">
        <span class="ag-leg ag-owns">{{ $t('argo.graph.legend.owns') }}</span>
        <span class="ag-leg ag-selects">{{ $t('argo.graph.legend.selects') }}</span>
        <span class="ag-leg ag-routes">{{ $t('argo.graph.legend.routes') }}</span>
      </span>
    </div>

    <div v-if="error" class="rd-empty">{{ error }}</div>
    <div v-else-if="loading && !graph" class="rd-empty">{{ $t('argo.graph.loading') }}</div>
    <template v-else-if="graph">
      <div v-if="graph.truncated" class="ag-note ag-warn">{{ $t('argo.graph.limited', { n: graph.nodes.length }) }}</div>
      <div v-for="w in graph.warnings" :key="w" class="ag-note ag-warn">{{ errorText(w) }}</div>

      <div v-if="layout.nodes.length > 1" class="ag-scroll">
        <svg :width="layout.width + 2" :height="layout.height + 2" class="ag-svg" role="img" :aria-label="$t('argo.graph.ariaLabel')">
          <path v-for="e in layout.edges" :key="e.id" :d="e.d" class="ag-edge" :class="'ag-edge-' + e.type" />
          <g
            v-for="n in layout.nodes"
            :key="n.id"
            class="ag-node"
            :class="[nodeTone(n), { 'ag-click': isClickable(n), 'ag-ghost': !n.managed && !n.isRoot }]"
            :transform="`translate(${n.x},${n.y})`"
            @click="onNodeClick(n)"
          >
            <title>{{ tooltip(n) }}</title>
            <rect :width="n.w" :height="n.h" rx="6" />
            <text x="10" y="19" class="ag-kind">{{ n.kind }}<tspan v-if="n.hook"> · {{ $t('argo.hook') }}</tspan></text>
            <text x="10" y="35" class="ag-name">{{ truncate(n.name, 26) }}</text>
            <text v-if="n.status === 'OutOfSync'" :x="n.w - 10" y="19" text-anchor="end" class="ag-oos">OutOfSync</text>
          </g>
        </svg>
      </div>
      <div v-else class="rd-empty">{{ $t('argo.resources.reported') }}</div>

      <div v-if="collapsedGroups.length" class="ag-note">
        {{ $t('argo.graph.collapsedGroups') }}
        <button v-for="g in collapsedGroups" :key="g.id" class="rd-btn ag-expand" @click="expand(g.id)">
          {{ g.name }}
        </button>
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { callApp } from '../../../resources/wails'
import { getResourceById } from '../../../resources/registry'
import { filterGraph, layoutGraph, needsAttention, GROUP_PREFIX, type PlacedNode } from '../../../resources/argoGraph'
import { errorText } from '../../../composables/errorText'
import { useDetailPanelSize } from '../../../composables/useDetailPanelSize'
import { argoLiveTick } from '../../../composables/useArgoLive'
import type { ArgoApplicationInfo, ArgoGraph } from '../../../types/argocd'

const props = defineProps<{ resource: Partial<ArgoApplicationInfo> }>()
const { t } = useI18n()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string; item?: any }): void
}>()

const { expanded, toggleExpanded } = useDetailPanelSize()

const graph = ref<ArgoGraph | null>(null)
const loading = ref(false)
const error = ref('')
const kindFilter = ref('')
const onlyProblems = ref(false)
const expandedGroups = ref<Set<string>>(new Set())   // было: expanded


let seq = 0
async function load() {
  const ns = props.resource?.namespace
  const name = props.resource?.name
  if (!ns || !name) return
  const my = ++seq
  loading.value = true
  try {
    const g = (await callApp('GetArgoApplicationGraph', ns, name)) as ArgoGraph
    if (my !== seq) return // устаревший ответ (быстрая смена приложения или события)
    graph.value = g
    error.value = ''
  } catch (e: any) {
    if (my !== seq) return
    error.value = errorText(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

onMounted(load)
// Приложение другое: сбросить граф и фильтры; то же приложение изменилось (sync/health/revision, argocd:changed) — перечитать
watch(() => `${props.resource?.namespace}/${props.resource?.name}`, () => {
  graph.value = null
  expandedGroups.value = new Set()
  load()
})
watch(
  () => [props.resource?.syncStatus, props.resource?.healthStatus, props.resource?.revision, argoLiveTick.value],
  load,
)

const kinds = computed(() => [...new Set((graph.value?.nodes ?? []).map(n => n.kind))].sort())

const isProblem = needsAttention

const filtered = computed<ArgoGraph | null>(() => {
  const g = graph.value
  if (!g) return null
  if (!kindFilter.value && !onlyProblems.value) return g
  return filterGraph(g, n =>
    (!kindFilter.value || n.kind === kindFilter.value) && (!onlyProblems.value || isProblem(n)))
})

const layout = computed(() =>
  layoutGraph(filtered.value ?? { nodes: [], edges: [], truncated: false, warnings: [] }, props.resource?.name ?? '', expandedGroups.value))

const collapsedGroups = computed(() => layout.value.nodes.filter(n => n.id.startsWith(GROUP_PREFIX)))

function expand(id: string) {
  expandedGroups.value = new Set(expandedGroups.value).add(id)
}

function nodeTone(n: PlacedNode): string {
  if (n.isRoot) return 'ag-root'
  switch (n.health) {
    case 'Healthy': return 'ag-ok'
    case 'Degraded': case 'Missing': return 'ag-err'
    case 'Progressing': case 'Suspended': return 'ag-warn-n'
    default: return n.status === 'OutOfSync' ? 'ag-warn-n' : 'ag-unk'
  }
}

/** Открывается только то, что приложение умеет показывать; схлопнутая группа и корень не кликабельны */
function target(n: PlacedNode): { id: string; ns: string; name: string } | null {
  if (n.isRoot || n.id.startsWith(GROUP_PREFIX)) return null
  const def = getResourceById(n.kind)
  if (!def || !n.name) return null
  if (def.namespaced && !n.namespace) return null
  return { id: def.id, ns: def.namespaced ? n.namespace : '', name: n.name }
}
const isClickable = (n: PlacedNode) => n.id.startsWith(GROUP_PREFIX) || !!target(n)
function onNodeClick(n: PlacedNode) {
  if (n.id.startsWith(GROUP_PREFIX)) return expand(n.id)
  const tgt = target(n)
  if (tgt) emit('open', tgt)
}

const tooltip = (n: PlacedNode) =>
  [`${n.kind} ${n.name}`, n.namespace && t('argo.graph.tip.namespace', { value: n.namespace }),
    n.status && t('argo.graph.tip.sync', { value: n.status }),
    n.health && t('argo.graph.tip.health', { value: n.health }), n.message].filter(Boolean).join('\n')

const truncate = (s: string, max: number) => (s.length > max ? s.slice(0, max - 1) + '…' : s)
</script>

<style scoped>
.ag-toolbar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.ag-select {
  padding: 5px 8px; background: var(--panel); color: var(--text-code); border: 1px solid var(--line-strong); border-radius: 4px; font: inherit;
}
.ag-check { display: flex; align-items: center; gap: 6px; color: var(--text-code); cursor: pointer; }
.ag-legend { margin-left: auto; display: flex; gap: 10px; font-size: 0.75rem; }
.ag-leg::before { content: ''; display: inline-block; width: 18px; height: 0; margin-right: 5px; vertical-align: middle; border-top: 2px solid; }
.ag-owns::before { border-color: var(--text-dim); }
.ag-selects::before { border-color: var(--blue); border-top-style: dashed; }
.ag-routes::before { border-color: #a78bfa; }
.ag-note { padding: 6px 0; font-size: 0.8rem; color: var(--gray-400); }
.ag-warn { color: var(--yellow); }
.ag-expand { margin-left: 6px; }
.ag-scroll { overflow: auto; max-height: calc(100vh - 340px); min-height: 240px; border: 1px solid #2a2f47; border-radius: 6px; padding: 10px; background: #12141f; }
.ag-svg { display: block; }

.ag-edge { fill: none; stroke: var(--text-dim); stroke-width: 1.4; }
.ag-edge-selects { stroke: var(--blue); stroke-dasharray: 5 4; }
.ag-edge-routes { stroke: #a78bfa; }

.ag-node rect { fill: #1a1d2e; stroke: var(--line-strong); stroke-width: 1.4; }
.ag-ok rect { stroke: var(--green); }
.ag-err rect { stroke: var(--red-soft); fill: rgba(248, 113, 113, 0.08); }
.ag-warn-n rect { stroke: var(--amber); }
.ag-unk rect { stroke: var(--text-dim); }
.ag-root rect { stroke: #8b5cf6; fill: rgba(139, 92, 246, 0.12); }
.ag-ghost rect { stroke-dasharray: 4 3; }
.ag-click { cursor: pointer; }
.ag-click:hover rect { fill: #20243a; }
.ag-kind { fill: #8b91ad; font-size: 10px; text-transform: uppercase; letter-spacing: 0.03em; }
.ag-name { fill: #e6e8f5; font-size: 12px; font-family: 'Courier New', monospace; }
.ag-oos { fill: var(--amber); font-size: 9px; font-weight: 600; }
</style>
