<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="lr">
    <div class="lr-title">{{ $t('defs.columns.limits') }}</div>
    <div v-if="!limits.length" class="rd-muted">{{ $t('sections.shared.noLimits') }}</div>
    <div v-else class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr>
            <th>{{ $t('defs.columns.type') }}</th><th>{{ $t('sections.shared.resource') }}</th><th>{{ $t('sections.shared.min') }}</th><th>{{ $t('sections.shared.max') }}</th>
            <th>{{ $t('defs.columns.default') }}</th><th>{{ $t('sections.limitRange.defaultRequest') }}</th><th>{{ $t('sections.limitRange.maxRatio') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(l, i) in limits" :key="i">
            <td>{{ l.type }}</td>
            <td class="rd-mono">{{ l.resource }}</td>
            <td>{{ dash(l.min) }}</td>
            <td>{{ dash(l.max) }}</td>
            <td>{{ dash(l.default) }}</td>
            <td>{{ dash(l.defaultRequest) }}</td>
            <td>{{ dash(l.maxLimitRequestRatio) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()
const limits = computed<any[]>(() => (Array.isArray(props.resource?.limits) ? props.resource.limits : []))
const dash = (v?: string) => (v ? v : '—')
</script>

<style scoped>
.lr-title { font-weight: 700; color: var(--text-strong); padding: 12px 0 8px; text-align: left; }
</style>
