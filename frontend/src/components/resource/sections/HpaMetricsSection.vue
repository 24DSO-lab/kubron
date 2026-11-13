<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hm">
    <div class="hm-title">{{ $t('defs.columns.metrics') }}</div>
    <div v-if="!metrics.length" class="rd-muted">{{ $t('sections.hpaMetrics.empty') }}</div>
    <div v-else class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr><th>{{ $t('defs.columns.type') }}</th><th>{{ $t('common.name') }}</th><th>{{ $t('defs.columns.current') }}</th><th>{{ $t('defs.columns.target') }}</th></tr>
        </thead>
        <tbody>
          <tr v-for="(m, i) in metrics" :key="i">
            <td>{{ m.type }}</td>
            <td class="rd-mono">{{ m.name }}</td>
            <td :class="{ 'rd-muted': m.current === 'unknown' }">{{ m.current }}</td>
            <td :class="{ 'rd-muted': m.target === 'unknown' }">{{ m.target }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()
const metrics = computed<any[]>(() => (Array.isArray(props.resource?.metrics) ? props.resource.metrics : []))
</script>

<style scoped>
.hm-title { font-weight: 700; color: var(--text-strong); padding: 12px 0 8px; text-align: left; }
</style>
