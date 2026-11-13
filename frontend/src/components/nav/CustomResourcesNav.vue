<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="crds.length || can(Perm.Read)" class="nav-group">
    <div class="nav-group-header" @click="open = !open">
      <span class="nav-arrow" :class="{ open }">›</span>
      <span class="nav-group-icon"></span>
      <span class="nav-group-title">{{ $t('nav.customResources') }}</span>
    </div>
    <div v-show="open" class="nav-group-items">
      <div :class="['nav-item', { active: currentView === 'crds' }]" @click="emit('navigate', 'crds')">
        <span class="nav-icon"></span> {{ $t('nav.definitions') }}
      </div>

      <input v-if="crds.length > 8" v-model="filter" class="cr-filter" type="search" :placeholder="$t('nav.filterKinds')" />

      <div v-for="g in groups" :key="g.name" class="nav-group">
        <div class="nav-group-header" @click="toggle(g.name)">
          <span class="nav-arrow" :class="{ open: isOpen(g.name) }">›</span>
          <span class="nav-group-title" :title="g.name">{{ g.name }}</span>
        </div>
        <div v-show="isOpen(g.name)" class="nav-group-items">
          <div
            v-for="c in g.items"
            :key="c.name"
            :class="['nav-item', { active: currentView === crKey(c) }]"
            :title="`${c.plural}.${c.group}`"
            @click="go(c)"
          >
            <span class="nav-icon"></span> {{ c.kind }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, reactive, ref } from 'vue'
import { can } from '../../composables/useAuth'
import { Perm } from '../../types/auth'
import { crds, crKey, ensureCustomResourceView } from '../../resources/customResources'
import type { CRDInfo } from '../../types/crd'

defineProps<{ currentView: string }>()
const emit = defineEmits<{ (e: 'navigate', view: string): void }>()

const open = ref(false)
const filter = ref('')
const expanded = reactive<Record<string, boolean>>({})

const groups = computed(() => {
  const q = filter.value.trim().toLowerCase()
  const by = new Map<string, CRDInfo[]>()
  for (const c of crds.value) {
    if (q && !`${c.kind} ${c.group}`.toLowerCase().includes(q)) continue
    by.set(c.group, [...(by.get(c.group) ?? []), c])
  }
  return [...by].map(([name, items]) => ({ name, items }))
})

// при фильтре группы раскрыты
const isOpen = (g: string) => !!filter.value.trim() || !!expanded[g]
const toggle = (g: string) => { expanded[g] = !expanded[g] }

function go(c: CRDInfo) {
  emit('navigate', ensureCustomResourceView(c))
}
</script>

<style scoped>
.cr-filter {
  width: calc(100% - 24px); margin: 4px 12px 6px; padding: 4px 8px; font-size: 12px;
  background: var(--surface-0); color: var(--text); border: 1px solid var(--surface-1); border-radius: 4px;
}
.cr-filter:focus { outline: none; border-color: var(--accent); }
.nav-group-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>