<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="rules.length" class="rules">
    <div class="rules-title">{{ $t('defs.columns.rules') }}</div>
    <table class="rules-table">
      <thead>
        <tr><th>{{ $t('sections.rules.apiGroups') }}</th><th>{{ $t('defs.tabs.resources') }}</th><th>{{ $t('sections.rules.verbs') }}</th></tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rules" :key="i">
          <td>{{ join(r.apiGroups) }}</td>
          <td>{{ join(r.resources ?? r.nonResourceURLs) }}</td>
          <td>{{ join(r.verbs) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()

const rules = computed<any[]>(() =>
  Array.isArray(props.resource?.rules) ? props.resource.rules : [],
)

function join(v: unknown, empty = '—'): string {
  if (!Array.isArray(v) || v.length === 0) return empty
  return v.map(x => (x === '' ? '(core)' : x)).join(', ')
}
</script>

<style scoped>
.rules-title { font-weight: bold; color: var(--text-code); margin-bottom: 8px; font-size: 0.875rem; }
.rules-table { width: 100%; border-collapse: collapse; font-size: 0.8125rem; }
.rules-table th, .rules-table td {
  text-align: left; vertical-align: top;
  padding: 6px 8px; border-bottom: 1px solid var(--line-strong);
}
.rules-table th { color: var(--text-gray); font-weight: 500; }
</style>
