<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="roleRef || subjects.length" class="binding">
    <div v-if="roleRef" class="meta-row">
      <span class="meta-label">{{ $t('sections.binding.role') }}</span>
      <a class="link" href="#" @click.prevent="openRole">{{ roleRef.kind }}/{{ roleRef.name }}</a>
    </div>

    <div v-if="subjects.length" class="meta-row">
      <span class="meta-label">{{ $t('sections.binding.subjects') }}</span>
      <ul class="subjects">
        <li v-for="(s, i) in subjects" :key="i">
          <a v-if="s.kind === 'ServiceAccount'" class="link" href="#" @click.prevent="openSubject(s)">
            {{ s.kind }}/{{ s.name }}
          </a>
          <span v-else>{{ s.kind }}/{{ s.name }}</span>
          <span v-if="s.namespace" class="ns"> ({{ s.namespace }})</span>
        </li>
      </ul>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()
const emit = defineEmits<{ (e: 'open', p: { id: string; ns: string; name: string }): void }>()

const ns = computed<string>(() => props.resource?.namespace ?? props.resource?.metadata?.namespace ?? '')
const roleRef = computed(() => props.resource?.roleRef ?? null)
const subjects = computed<any[]>(() => (Array.isArray(props.resource?.subjects) ? props.resource.subjects : []))

function openRole() {
  const r = roleRef.value
  if (!r) return
  if (r.kind === 'ClusterRole') emit('open', { id: 'clusterrole', ns: '', name: r.name })
  else emit('open', { id: 'role', ns: ns.value, name: r.name })
}

function openSubject(s: any) {
  emit('open', { id: 'serviceaccount', ns: s.namespace || ns.value, name: s.name })
}
</script>

<style scoped>
.binding { display: flex; flex-direction: column; gap: 12px; }
.meta-row { display: grid; grid-template-columns: auto 1fr; align-items: baseline; gap: 12px; font-size: 0.875rem; }
.meta-label { font-weight: bold; color: var(--text-code); white-space: nowrap; }
.subjects { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 4px; }
.link { color: #6aa8ff; cursor: pointer; text-decoration: none; }
.link:hover { text-decoration: underline; }
.ns { color: var(--text-gray); }
</style>
