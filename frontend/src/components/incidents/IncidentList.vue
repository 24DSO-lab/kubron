<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="list" data-testid="incident-list">
    <div class="filters" role="group" :aria-label="$t('incidents.list.title')">
      <button v-for="f in FILTERS" :key="f" type="button" class="btn btn-secondary" :class="{ active: status === f }"
        :data-testid="`filter-${f || 'all'}`" @click="emit('filter', f)">
        {{ $t(`incidents.list.filter.${f || 'all'}`) }}
      </button>
    </div>
    <p v-if="!items.length && !loading" class="muted" data-testid="list-empty">{{ $t('incidents.list.empty') }}</p>
    <table v-else class="tbl">
      <thead>
        <tr>
          <th>{{ $t('incidents.list.columns.target') }}</th>
          <th>{{ $t('incidents.list.columns.symptoms') }}</th>
          <th>{{ $t('incidents.list.columns.opened') }}</th>
          <th>{{ $t('incidents.list.columns.status') }}</th>
          <th>{{ $t('incidents.list.columns.result') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="i in items" :key="i.id" class="row" data-testid="incident-row" :data-id="i.id" tabindex="0"
          @click="emit('select', i.id)" @keydown.enter="emit('select', i.id)">
          <td>{{ i.target.kind }}/{{ i.target.namespace }}/{{ i.target.name }}</td>
          <td>{{ i.symptoms.map((s) => codeText('symptoms', s)).join(', ') }}</td>
          <td class="nowrap">{{ when(i.openedAt) }}</td>
          <td><span class="badge" :class="`st-${i.status}`" data-testid="incident-status">{{ $t(`incidents.status.${i.status}`) }}</span></td>
          <td>{{ i.result ? $t(`incidents.outcome.results.${i.result}`) : '' }}</td>
        </tr>
      </tbody>
    </table>
    <button v-if="hasMore" class="btn btn-secondary" type="button" :disabled="loading" data-testid="list-more" @click="emit('more')">
      {{ $t('incidents.list.more') }}
    </button>
  </div>
</template>

<script lang="ts" setup>
import { codeText } from '../../composables/incidentModel'
import type { IncidentStatus, IncidentSummary } from '../../types/incident'

defineProps<{ items: IncidentSummary[]; status: IncidentStatus | ''; loading?: boolean; hasMore?: boolean }>()
const emit = defineEmits<{
  (e: 'select', id: string): void
  (e: 'filter', s: IncidentStatus | ''): void
  (e: 'more'): void
}>()

const FILTERS: Array<IncidentStatus | ''> = ['', 'open', 'closed']

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<style scoped>
.list { display: flex; flex-direction: column; gap: 12px; }
.filters { display: flex; gap: 6px; }
.filters .active { outline: 1px solid #89b4fa; }
.tbl { width: 100%; border-collapse: collapse; font-size: 13px; }
.tbl th { text-align: left; padding: 8px 12px; color: #a6adc8; border-bottom: 1px solid #313244; }
.tbl td { padding: 9px 12px; border-bottom: 1px solid #232334; }
.row { cursor: pointer; }
.row:hover, .row:focus { background: #232334; outline: none; }
.nowrap { white-space: nowrap; }
.muted { color: #6c7086; font-size: 13px; }
.badge { padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
.st-open { background: rgba(249, 226, 175, 0.15); color: #f9e2af; }
.st-closed { background: rgba(166, 227, 161, 0.15); color: #a6e3a1; }
</style>
