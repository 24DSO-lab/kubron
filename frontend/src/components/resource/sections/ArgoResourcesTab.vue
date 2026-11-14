<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div>
    <div v-if="all.length" class="argo-toolbar">
      <input v-model="search" class="argo-search" type="search" :placeholder="$t('argo.resources.filterPlaceholder')" />
      <label class="argo-check">
        <input v-model="onlyProblems" type="checkbox" /> {{ $t('argo.resources.onlyProblems') }}
      </label>
      <span class="rd-muted">{{ filtered.length }} / {{ all.length }}</span>
    </div>

    <div v-if="visible.length" class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr><th>{{ $t('defs.columns.kind') }}</th><th>{{ $t('common.name') }}</th><th>{{ $t('common.namespace') }}</th><th>{{ $t('argo.columns.sync') }}</th><th>{{ $t('argo.columns.health') }}</th><th>{{ $t('defs.columns.message') }}</th></tr>
        </thead>
        <tbody>
          <tr
            v-for="r in visible"
            :key="rowKey(r)"
            :class="{ 'rd-click': !!target(r) }"
            @click="openRef(r)"
          >
            <td>{{ r.kind }}<span v-if="r.hook" class="rd-muted"> ({{ $t('argo.hook') }})</span></td>
            <td :class="{ 'rd-link': !!target(r) }">{{ r.name }}</td>
            <td class="rd-muted">{{ r.namespace || '—' }}</td>
            <td><span class="rd-badge" :class="badgeClass(r.status)">{{ r.status || $t('common.unknown') }}</span></td>
            <td>
              <span v-if="r.health" class="rd-badge" :class="badgeClass(r.health)">{{ r.health }}</span>
              <span v-else class="rd-muted">—</span>
            </td>
            <td class="rd-muted">{{ r.message || '' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="rd-empty">{{ all.length ? $t('argo.resources.noMatch') : $t('argo.resources.reported') }}</div>

    <!-- status.resources у больших приложений — тысячи строк: рисуем порциями -->
    <div v-if="filtered.length > limit" class="argo-more">
      <button class="rd-btn" @click="limit += PAGE">{{ $t('argo.resources.showMore', { n: filtered.length - limit }) }}</button>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { badgeClass } from '../../../resources/status'
import { getResourceById } from '../../../resources/registry'
import type { ArgoApplicationInfo, ArgoResourceRef } from '../../../types/argocd'

const PAGE = 200

const props = defineProps<{ resource: Partial<ArgoApplicationInfo> }>()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string; item?: any }): void
}>()

const search = ref('')
const onlyProblems = ref(false)
const limit = ref(PAGE)

const all = computed(() => props.resource?.resources ?? [])
const isProblem = (r: ArgoResourceRef) =>
  r.status === 'OutOfSync' || (!!r.health && r.health !== 'Healthy' && r.health !== 'Suspended')

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return all.value.filter(r => {
    if (onlyProblems.value && !isProblem(r)) return false
    if (!q) return true
    return `${r.kind} ${r.name} ${r.namespace}`.toLowerCase().includes(q)
  })
})
const visible = computed(() => filtered.value.slice(0, limit.value))

watch([search, onlyProblems], () => { limit.value = PAGE })

const rowKey = (r: ArgoResourceRef) => `${r.group}/${r.kind}/${r.namespace}/${r.name}`

/** Открываем только те kind, которые приложение умеет показывать, и только namespaced с namespace */
function target(r: ArgoResourceRef): { id: string; ns: string; name: string } | null {
  const def = getResourceById(r.kind)
  if (!def || !r.name) return null
  if (def.namespaced && !r.namespace) return null
  return { id: def.id, ns: def.namespaced ? r.namespace : '', name: r.name }
}

function openRef(r: ArgoResourceRef) {
  const t = target(r)
  if (t) emit('open', t)
}
</script>

<style scoped>
.argo-toolbar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.argo-search {
  flex: 1; min-width: 180px; padding: 6px 10px; background: var(--panel); color: var(--text-code);
  border: 1px solid var(--line-strong); border-radius: 4px; font: inherit;
}
.argo-check { display: flex; align-items: center; gap: 6px; color: var(--text-code); cursor: pointer; }
.argo-more { padding: 10px 0; text-align: center; }
</style>
