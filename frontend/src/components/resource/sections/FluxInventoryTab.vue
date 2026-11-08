<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="flux-inventory" data-testid="flux-inventory">
    <div v-if="loading" class="rd-empty" role="status" data-testid="flux-inventory-loading">{{ $t('flux.inventory.loading') }}</div>
    <div v-else-if="error" class="rd-empty" role="alert" data-testid="flux-inventory-error">{{ error }}</div>
    <div v-else-if="mode === 'unavailable'" class="rd-empty" data-testid="flux-inventory-unavailable">{{ $t('flux.inventory.unavailable') }}</div>
    <div v-else-if="mode === 'remote'" class="rd-empty" data-testid="flux-inventory-remote">{{ $t('flux.inventory.remote') }}</div>
    <div v-else-if="!all.length" class="rd-empty" data-testid="flux-inventory-empty">{{ $t('flux.inventory.empty') }}</div>

    <template v-else>
      <div class="flux-toolbar">
        <input
          v-model="search"
          class="flux-search"
          type="search"
          :placeholder="$t('flux.inventory.filterPlaceholder')"
          :aria-label="$t('flux.inventory.filterPlaceholder')"
          data-testid="flux-inventory-search"
        />
        <select
          v-model="kind"
          class="flux-kind"
          :aria-label="$t('flux.inventory.kindFilter')"
          data-testid="flux-inventory-kind"
        >
          <option value="">{{ $t('flux.inventory.allKinds') }}</option>
          <option v-for="k in kinds" :key="k" :value="k">{{ k }}</option>
        </select>
        <span class="rd-muted">{{ shownCount }} / {{ all.length }}</span>
        <span v-if="graph?.truncated" class="rd-badge rd-warn" data-testid="flux-inventory-truncated">{{ $t('flux.inventory.truncated') }}</span>
      </div>

      <div v-if="!split.workloads.length && !split.others.length" class="rd-empty" data-testid="flux-inventory-nomatch">
        {{ $t('flux.inventory.noMatch') }}
      </div>

      <section v-for="g in groups" :key="g.key" class="flux-group" :data-testid="`flux-inventory-${g.key}`">
        <h4 class="flux-group-title">{{ $t(`flux.inventory.${g.key}`) }} <span class="rd-muted">({{ g.items.length }})</span></h4>
        <div class="rd-table-wrap">
          <table class="rd-table">
            <thead>
              <tr>
                <th>{{ $t('defs.columns.kind') }}</th>
                <th>{{ $t('common.name') }}</th>
                <th>{{ $t('common.namespace') }}</th>
                <th>{{ $t('flux.inventory.health') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="n in g.visible"
                :key="n.id"
                :class="{ 'rd-click': !!target(n) }"
                data-testid="flux-inventory-row"
                @click="openNode(n)"
              >
                <td>{{ n.kind }}</td>
                <td>
                  <button v-if="target(n)" type="button" class="flux-linkbtn" @click.stop="openNode(n)">{{ n.name }}</button>
                  <template v-else>{{ n.name }}</template>
                </td>
                <td class="rd-muted">{{ n.namespace || $t('common.dash') }}</td>
                <td>
                  <span v-if="n.state" class="rd-badge" :class="badgeClass(n.state)">{{ n.state }}</span>
                  <span v-else class="rd-muted">{{ $t('common.dash') }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- inventory у больших объектов — сотни строк: рисуем порциями -->
      <div v-if="hiddenCount > 0" class="flux-more">
        <button type="button" class="rd-btn" data-testid="flux-inventory-more" @click="limit += PAGE">
          {{ $t('flux.inventory.showMore', { n: hiddenCount }) }}
        </button>
      </div>
    </template>
  </div>
</template>

<script lang="ts">
import type { FluxGraph, FluxGraphNode } from '../../../types/flux'

/** Рабочие нагрузки: то, что пользователь ищет в списке ресурсов в первую очередь */
export const WORKLOAD_KINDS: ReadonlySet<string> = new Set([
  'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod',
])

/** Что показать вместо списка: нет данных, граф недоступен (Flux < 2.8), ресурсы в другом кластере или список */
export type InventoryMode = 'none' | 'unavailable' | 'remote' | 'list'

export function inventoryMode(g: FluxGraph | null | undefined): InventoryMode {
  if (!g) return 'none'
  if (!g.available) return 'unavailable'
  if (g.remote) return 'remote'
  return 'list'
}

/** Ресурсы объекта: узлы графа с ролью resource (источник и сам объект — другие роли) */
export function resourceNodes(g: FluxGraph | null | undefined): FluxGraphNode[] {
  return (g?.nodes ?? []).filter(n => n?.role === 'resource' && !!n.name)
}

const byIdentity = (a: FluxGraphNode, b: FluxGraphNode) =>
  a.kind.localeCompare(b.kind) || a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name)

/** Фильтр по kind и подстроке (kind, имя, namespace) и разбиение на рабочие нагрузки и прочее; порядок стабильный */
export function splitInventory(
  nodes: readonly FluxGraphNode[],
  query = '',
  kind = '',
): { workloads: FluxGraphNode[]; others: FluxGraphNode[] } {
  const q = query.trim().toLowerCase()
  const kept = nodes
    .filter(n => !kind || n.kind === kind)
    .filter(n => !q || `${n.kind} ${n.name} ${n.namespace}`.toLowerCase().includes(q))
    .sort(byIdentity)
  return {
    workloads: kept.filter(n => WORKLOAD_KINDS.has(n.kind)),
    others: kept.filter(n => !WORKLOAD_KINDS.has(n.kind)),
  }
}
</script>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { errorText } from '../../../composables/errorText'
import { fluxLiveTick } from '../../../composables/useFluxLive'
import { getResourceById } from '../../../resources/registry'
import { badgeClass } from '../../../resources/status'
import { callApp } from '../../../resources/wails'

import type { FluxObjectInfo } from '../../../types/flux'

const PAGE = 200

// Ресурсы объекта берутся из графа цепочки (GetFluxGraph): один источник правды с вкладкой «Граф»
const props = defineProps<{ resource: Partial<FluxObjectInfo> }>()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string; item?: any }): void
}>()

const graph = ref<FluxGraph | null>(null)
const loading = ref(false)
const error = ref('')
const search = ref('')
const kind = ref('')
const limit = ref(PAGE)


/** Номер запроса: ответ по уже закрытому или сменившемуся объекту отбрасывается */
let generation = 0

async function load() {
  const r = props.resource
  const mine = ++generation
  if (!r?.kind || !r.name) {
    graph.value = null
    loading.value = false
    return
  }
  // фоновое обновление по flux:changed не мигает индикатором загрузки: он нужен только пока данных нет
  loading.value = graph.value === null
  error.value = ''
  try {
    const g = await callApp('GetFluxGraph', { kind: r.kind, namespace: r.namespace ?? '', name: r.name })
    if (mine !== generation) return
    graph.value = g as unknown as FluxGraph
  } catch (e) {
    if (mine !== generation) return
    graph.value = null
    error.value = errorText(e)
  } finally {
    if (mine === generation) loading.value = false
  }
}

// Другой объект: сбрасываем и данные, и фильтры, чтобы не показать ресурсы прошлого
watch(
  () => [props.resource?.kind, props.resource?.namespace, props.resource?.name],
  () => {
    graph.value = null
    search.value = ''
    kind.value = ''
    void load()
  },
  { immediate: true },
)
// Изменение объектов Flux: перечитываем граф того же объекта
watch(fluxLiveTick, () => { void load() })
watch([search, kind], () => { limit.value = PAGE })

const mode = computed(() => inventoryMode(graph.value))
const all = computed(() => resourceNodes(graph.value))
const kinds = computed(() => [...new Set(all.value.map(n => n.kind))].sort((a, b) => a.localeCompare(b)))
const split = computed(() => splitInventory(all.value, search.value, kind.value))

/** Показываемые порции: лимит общий для обеих групп, рабочие нагрузки идут первыми */
const groups = computed(() => {
  let left = limit.value
  return (['workloads', 'others'] as const)
    .map(key => {
      const items = split.value[key]
      const visible = items.slice(0, Math.max(0, left))
      left -= visible.length
      return { key, items, visible }
    })
    .filter(g => g.items.length)
})
const shownCount = computed(() => split.value.workloads.length + split.value.others.length)
const hiddenCount = computed(() => groups.value.reduce((s, g) => s + g.items.length - g.visible.length, 0))

/** Открываем только те kind, которые приложение умеет показывать, и namespaced — только с namespace */
function target(n: FluxGraphNode): { id: string; ns: string; name: string } | null {
  const def = getResourceById(n.kind)
  if (!def || !n.name) return null
  if (def.namespaced && !n.namespace) return null
  return { id: def.id, ns: def.namespaced ? n.namespace : '', name: n.name }
}

function openNode(n: FluxGraphNode) {
  const t = target(n)
  if (t) emit('open', t)
}
</script>

<style scoped>
.flux-toolbar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.flux-search, .flux-kind {
  padding: 6px 10px; background: #1a1b2e; color: #d4d4d4; border: 1px solid #3b3d5c; border-radius: 4px; font: inherit;
}
.flux-search { flex: 1; min-width: 180px; }
.flux-group { margin-top: 12px; }
.flux-group-title { margin: 0 0 6px; font-size: 0.95rem; color: #d4d4d4; }
.flux-more { padding: 10px 0; text-align: center; }
.flux-linkbtn {
  background: none; border: 0; padding: 0; font: inherit; color: #60a5fa; cursor: pointer; text-decoration: underline;
}
.flux-linkbtn:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; border-radius: 2px; }
</style>
