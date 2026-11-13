<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="sel">
    <div class="sel-title">{{ $t('defs.columns.selector') }}</div>

    <div class="rd-row">
      <span class="rd-label">{{ $t('sections.pvcSelector.matchLabels') }}</span>
      <span v-if="labels.length" class="rd-value sel-chips">
        <span v-for="[k, v] in labels" :key="k" class="sel-chip">{{ k }}={{ v }}</span>
      </span>
      <span v-else class="rd-muted">—</span>
    </div>

    <div class="rd-row">
      <span class="rd-label">{{ $t('sections.pvcSelector.matchExpressions') }}</span>
      <span v-if="expressions.length" class="rd-value sel-chips">
        <span v-for="(e, i) in expressions" :key="i" class="sel-chip">{{ format(e) }}</span>
      </span>
      <span v-else class="rd-muted">—</span>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()

const labels = computed(() => Object.entries(props.resource?.selector?.matchLabels ?? {}) as [string, string][])
const expressions = computed<any[]>(() => props.resource?.selector?.matchExpressions ?? [])

function format(e: { key: string; operator: string; values?: string[] }): string {
  return e.values?.length ? `${e.key} ${e.operator} (${e.values.join(', ')})` : `${e.key} ${e.operator}`
}
</script>

<style scoped>
.sel { margin-top: 16px; }
.sel-title { font-weight: 700; font-size: 1rem; color: #fff; padding: 8px 0; text-align: left; }
.sel-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.sel-chip {
  padding: 3px 8px; background: var(--panel); border-radius: 3px;
  font-family: 'Courier New', monospace; font-size: 0.8rem; color: var(--text-code);
}
</style>