<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <!-- Графа нет или он пуст (старый отчёт): карта скрыта без ошибки. -->
  <section v-if="!model.empty" ref="root" class="map" data-testid="dependency-map" :aria-label="$t('incidentsMap.title')">
    <header class="top">
      <h3>{{ $t('incidentsMap.title') }}</h3>
      <span class="summary" data-testid="map-summary">{{ $t('incidentsMap.summary', { nodes: model.nodes.length, edges: model.edges.length }) }}</span>
    </header>
    <p class="note">{{ $t('incidentsMap.note') }}</p>
    <p v-if="model.incomplete" class="warn" role="status" data-testid="map-incomplete"><span aria-hidden="true">!</span> {{ $t('incidentsMap.incomplete') }}</p>

    <div class="toolbar">
      <div class="zoom" role="group" :aria-label="$t('incidentsMap.controls.fit')">
        <button type="button" data-testid="map-zoom-in" :aria-label="$t('incidentsMap.controls.zoomIn')" :title="$t('incidentsMap.controls.zoomIn')" @click="zoomBy(1.25)">+</button>
        <button type="button" data-testid="map-zoom-out" :aria-label="$t('incidentsMap.controls.zoomOut')" :title="$t('incidentsMap.controls.zoomOut')" @click="zoomBy(0.8)">−</button>
        <button type="button" data-testid="map-fit" :aria-label="$t('incidentsMap.controls.fit')" :title="$t('incidentsMap.controls.fit')" @click="fit">⤢</button>
      </div>
      <label v-if="hasChain" class="check"><input v-model="highlightOn" type="checkbox" data-testid="map-highlight" /> {{ $t('incidentsMap.controls.highlight') }}</label>
      <button v-if="hasGroups" type="button" class="link" data-testid="map-expand-all" @click="expandAll">{{ $t('incidentsMap.controls.expandAll') }}</button>
    </div>

    <details class="filters" data-testid="map-filters">
      <summary>{{ $t('incidentsMap.filters.title') }}</summary>
      <fieldset>
        <legend>{{ $t('incidentsMap.filters.kind') }}</legend>
        <label v-for="k in model.kinds" :key="k" class="check">
          <input type="checkbox" :checked="kindShown(k)" :data-kind="k" @change="toggleKind(k)" /> {{ k }}
        </label>
      </fieldset>
      <fieldset>
        <legend>{{ $t('incidentsMap.filters.health') }}</legend>
        <label v-for="h in model.healths" :key="h" class="check">
          <input type="checkbox" :checked="healthShown(h)" :data-health="h" @change="toggleHealth(h)" /> {{ $t(`incidentsMap.health.${h}`) }}
        </label>
      </fieldset>
      <button v-if="filterActive" type="button" class="link" data-testid="map-filter-reset" @click="resetFilters">{{ $t('incidentsMap.filters.reset') }}</button>
    </details>

    <div class="body">
      <div class="canvas" data-testid="map-canvas" role="group" :aria-label="$t('incidentsMap.aria')" @keydown="onKeydown">
        <svg ref="svg" :viewBox="`0 0 ${Math.max(layout.width, 1)} ${Math.max(layout.height, 1)}`" preserveAspectRatio="xMidYMid meet" class="svg"
          @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="endDrag" @pointercancel="endDrag" @wheel.prevent="onWheel" @click="clearSelection">
          <defs>
            <marker v-for="s in MARKER_STATES" :id="`${uid}-${s}`" :key="s" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" :class="`arrow s-${s}`" />
            </marker>
          </defs>
          <g :transform="`translate(${tx} ${ty}) scale(${scale})`" data-testid="map-viewport">
            <MapEdge v-for="e in visible.edges" :key="e.key" :edge="e" :marker-prefix="uid" :chain="highlight.edges.has(e.key)"
              :dim="dimEdge(e)" :label="edgeLabel(e)" v-bind="edgeGeometry(e)" />
            <MapNode v-for="n in visible.nodes" :key="n.key" :node="n" :x="layout.positions[n.key].x" :y="layout.positions[n.key].y"
              :selected="n.key === selectedKey" :chain="highlight.nodes.has(n.key)" :dim="dimNode(n)" :focusable="n.key === tabStop"
              @select="onSelect" @focus="focusKey = $event" />
          </g>
        </svg>
      </div>
      <MapDetails :report="report" :model="model" :node="selectedNode" @clear="clearSelection" @expand="expandKind" />
    </div>
    <MapLegend />
  </section>
</template>

<script lang="ts" setup>
import { computed, nextTick, ref, useId, watch } from 'vue'
import {
  MAP_EDGE_TYPES, buildMapModel, collapseModel, filterModel, highlightChains, isFilterActive, isGroupKey, isNavKey, navigate,
  refKey, type MapEdge as MapEdgeModel, type MapHealth, type MapNode as MapNodeModel,
} from '../../../composables/dependencyMap'
import { edgePath, layoutMap } from '../../../composables/dependencyMapLayout'
import { useI18n } from 'vue-i18n'
import type { Report, ResourceRef } from '../../../types/incident'
import MapDetails from './MapDetails.vue'
import MapEdge from './MapEdge.vue'
import MapLegend from './MapLegend.vue'
import MapNode from './MapNode.vue'

/**
 * Интерактивная карта зависимостей расследования (ADR-0031 §8).
 * Вход — отчёт и выбранный ресурс (`ResourceRef` или ключ вершины «group/kind/namespace/name»);
 * выход — событие `select` с `ResourceRef` выбранного ресурса или `null` при снятии выбора.
 * Сворачиваемые группы не являются ресурсами и событие `select` не вызывают.
 */
const props = defineProps<{
  report?: Report | null
  selected?: ResourceRef | string | null
}>()
const emit = defineEmits<{ (e: 'select', ref: ResourceRef | null): void }>()
const { t } = useI18n()

const uid = `dm${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
const MARKER_STATES = ['ok', 'broken', 'missing', 'unknown'] as const
const MIN_SCALE = 0.2
const MAX_SCALE = 4

const model = computed(() => buildMapModel(props.report))
const hasChain = computed(() => model.value.candidateKeys.length > 0 || (props.report?.chains?.length ?? 0) > 0)

// ---- Состояние интерфейса ----
const highlightOn = ref(true)
const shownKinds = ref<ReadonlySet<string> | null>(null)
const shownHealths = ref<ReadonlySet<MapHealth> | null>(null)
const expandedKinds = ref<ReadonlySet<string>>(new Set())
const selectedKey = ref('')
const focusKey = ref('')
const scale = ref(1)
const tx = ref(0)
const ty = ref(0)
const root = ref<HTMLElement | null>(null)
const svg = ref<SVGSVGElement | null>(null)

const propKey = (s: ResourceRef | string | null | undefined) => (typeof s === 'string' ? s : s ? refKey(s) : '')
watch(() => props.selected, (s) => { selectedKey.value = propKey(s) }, { immediate: true })
// Новый отчёт — чистый вид: фильтры и приближение сбрасываются, выбор остаётся по ключу.
watch(() => props.report, () => { shownKinds.value = null; shownHealths.value = null; expandedKinds.value = new Set(); fit() })

const highlight = computed(() => (highlightOn.value ? highlightChains(model.value, props.report) : { nodes: new Set<string>(), edges: new Set<string>() }))
const keep = computed(() => new Set([...highlight.value.nodes, ...(selectedKey.value ? [selectedKey.value] : [])]))
const filter = computed(() => ({ kinds: shownKinds.value, healths: shownHealths.value }))
const filterActive = computed(() => isFilterActive(filter.value))
const visible = computed(() => collapseModel(filterModel(model.value, filter.value, keep.value), expandedKinds.value, keep.value))
const layout = computed(() => layoutMap(visible.value.nodes, visible.value.edges))
const hasGroups = computed(() => visible.value.nodes.some((n) => n.variant === 'group'))

const selectedNode = computed<MapNodeModel | null>(() => {
  const k = selectedKey.value
  if (!k) return null
  return visible.value.nodes.find((n) => n.key === k) ?? model.value.nodes.find((n) => n.key === k) ?? null
})
const tabStop = computed(() => (layout.value.positions[focusKey.value] ? focusKey.value : (visible.value.nodes[0]?.key ?? '')))

// ---- Приглушение: при подсветке цепочки остальное затемняется ----
const chainActive = computed(() => highlight.value.nodes.size > 0)
const dimNode = (n: MapNodeModel) => chainActive.value && !highlight.value.nodes.has(n.key) && n.key !== selectedKey.value
const dimEdge = (e: MapEdgeModel) => chainActive.value && !highlight.value.edges.has(e.key)
/** Подписи типов связей — только у подсвеченных и у связей выбранной вершины (экономит место на больших графах). */
function edgeLabel(e: MapEdgeModel): string {
  const on = highlight.value.edges.has(e.key) || (!!selectedKey.value && (e.from === selectedKey.value || e.to === selectedKey.value))
  return on ? t(`incidentsMap.edge.${(MAP_EDGE_TYPES as readonly string[]).includes(e.type) ? e.type : 'other'}`) : ''
}
function edgeGeometry(e: MapEdgeModel) {
  const p = edgePath(layout.value, e.from, e.to)
  return { d: p?.d ?? '', mx: p?.mx ?? 0, my: p?.my ?? 0 }
}

// ---- Фильтры ----
const kindShown = (k: string) => shownKinds.value === null || shownKinds.value.has(k)
const healthShown = (h: MapHealth) => shownHealths.value === null || shownHealths.value.has(h)
function toggle<T>(current: ReadonlySet<T> | null, all: T[], item: T): ReadonlySet<T> | null {
  const next = new Set(current ?? all)
  if (next.has(item)) next.delete(item)
  else next.add(item)
  return next.size === all.length ? null : next
}
const toggleKind = (k: string) => { shownKinds.value = toggle(shownKinds.value, model.value.kinds, k) }
const toggleHealth = (h: MapHealth) => { shownHealths.value = toggle(shownHealths.value, model.value.healths, h) }
const resetFilters = () => { shownKinds.value = null; shownHealths.value = null }
const expandKind = (kind: string) => { expandedKinds.value = new Set([...expandedKinds.value, kind]); selectedKey.value = '' }
const expandAll = () => { expandedKinds.value = new Set(model.value.kinds); selectedKey.value = '' }

// ---- Выбор ----
function onSelect(key: string) {
  if (selectedKey.value === key) return clearSelection()
  selectedKey.value = key
  focusKey.value = key
  if (isGroupKey(key)) return
  const n = model.value.nodes.find((x) => x.key === key)
  if (n) emit('select', { group: n.group === 'core' ? undefined : n.group, kind: n.kind, namespace: n.namespace || undefined, name: n.name })
}
function clearSelection() {
  if (!selectedKey.value) return
  const wasGroup = isGroupKey(selectedKey.value)
  selectedKey.value = ''
  if (!wasGroup) emit('select', null)
}

// ---- Клавиатура: стрелки — перемещение по раскладке, Enter/пробел — выбор, Esc — снять выбор ----
function focusNode(key: string) {
  focusKey.value = key
  void nextTick(() => {
    const el = Array.from(root.value?.querySelectorAll<SVGElement>('[data-testid="map-node"]') ?? []).find((e) => e.getAttribute('data-key') === key)
    el?.focus()
  })
}
function onKeydown(ev: KeyboardEvent) {
  if (isNavKey(ev.key)) {
    ev.preventDefault()
    const next = navigate(layout.value.positions, focusKey.value || tabStop.value, ev.key)
    if (next) focusNode(next)
  } else if ((ev.key === 'Enter' || ev.key === ' ') && focusKey.value && layout.value.positions[focusKey.value]) {
    ev.preventDefault()
    onSelect(focusKey.value)
  } else if (ev.key === 'Escape') {
    clearSelection()
  }
}

// ---- Масштаб и сдвиг: без анимации (prefers-reduced-motion) ----
const clampScale = (s: number) => Math.min(MAX_SCALE, Math.max(MIN_SCALE, s))
function zoomAt(factor: number, cx: number, cy: number) {
  const next = clampScale(scale.value * factor)
  const k = next / scale.value
  tx.value = cx - (cx - tx.value) * k
  ty.value = cy - (cy - ty.value) * k
  scale.value = next
}
const zoomBy = (factor: number) => zoomAt(factor, layout.value.width / 2, layout.value.height / 2)
function fit() { scale.value = 1; tx.value = 0; ty.value = 0 }
/** Пиксели экрана в единицы viewBox (preserveAspectRatio=meet: общий масштаб по большей стороне). */
function unitsPerPixel(): number {
  const r = svg.value?.getBoundingClientRect()
  if (!r || !r.width || !r.height) return 1
  return Math.max(layout.value.width / r.width, layout.value.height / r.height)
}
function onWheel(ev: WheelEvent) {
  const r = svg.value?.getBoundingClientRect()
  if (!r) return
  const f = unitsPerPixel()
  const cx = (ev.clientX - r.left) * f
  const cy = (ev.clientY - r.top) * f
  zoomAt(ev.deltaY < 0 ? 1.1 : 1 / 1.1, cx, cy)
}
let drag: { x: number; y: number } | null = null
function onPointerDown(ev: PointerEvent) {
  if ((ev.target as Element).closest('[data-testid="map-node"]')) return
  drag = { x: ev.clientX, y: ev.clientY }
  svg.value?.setPointerCapture?.(ev.pointerId)
}
function onPointerMove(ev: PointerEvent) {
  if (!drag) return
  const f = unitsPerPixel()
  tx.value += (ev.clientX - drag.x) * f
  ty.value += (ev.clientY - drag.y) * f
  drag = { x: ev.clientX, y: ev.clientY }
}
function endDrag() { drag = null }
</script>

<style scoped>
.map { display: grid; gap: 8px; color: var(--text, #cdd6f4); }
.top { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; }
h3 { margin: 0; font-size: 15px; }
.summary, .note { color: var(--text-muted, #a6adc8); font-size: 12px; }
.note { margin: 0; }
.warn { margin: 0; padding: 6px 10px; border: 1px dashed var(--amber, #fab387); border-radius: 6px; color: var(--amber, #fab387); font-size: 12px; }
.toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; font-size: 12px; }
.zoom { display: inline-flex; gap: 4px; }
.zoom button, .link { padding: 2px 9px; border: 1px solid var(--line-strong, #45475a); border-radius: 6px; background: transparent; color: var(--text, #cdd6f4); cursor: pointer; font-size: 13px; }
.link { font-size: 12px; }
.zoom button:focus-visible, .link:focus-visible, summary:focus-visible { outline: 2px solid var(--accent, #89b4fa); outline-offset: 2px; }
.check { display: inline-flex; align-items: center; gap: 4px; margin-right: 10px; font-size: 12px; }
.filters { font-size: 12px; color: var(--text-muted, #a6adc8); }
.filters summary { cursor: pointer; }
fieldset { margin: 6px 0; padding: 4px 8px; border: 1px solid var(--line, #2a2b40); border-radius: 6px; }
legend { padding: 0 4px; font-size: 12px; }
.body { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-start; }
.canvas { flex: 1 1 480px; min-width: 0; height: 460px; overflow: hidden; border: 1px solid var(--line-strong, #45475a); border-radius: 8px; background: var(--bg, #1e1e2e); }
.svg { width: 100%; height: 100%; cursor: grab; touch-action: none; user-select: none; }
.svg:active { cursor: grabbing; }
.arrow { fill: var(--text-faint, #7f849c); }
.arrow.s-broken { fill: var(--amber, #fab387); }
.arrow.s-missing { fill: var(--danger, #f38ba8); }
</style>
