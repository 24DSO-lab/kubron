<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="events" data-testid="events-snapshot">
    <p v-if="!events.length" class="muted" data-testid="events-empty">{{ $t('incidents.events.empty') }}</p>
    <div v-else class="scroll">
      <table class="tbl">
        <thead>
          <tr>
            <th>{{ $t('incidents.events.columns.time') }}</th>
            <th>{{ $t('incidents.events.columns.object') }}</th>
            <th>{{ $t('incidents.events.columns.reason') }}</th>
            <th>{{ $t('incidents.events.columns.message') }}</th>
            <th>{{ $t('incidents.events.columns.count') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(e, i) in events" :key="i" data-testid="event-row" :class="{ warn: e.type === 'Warning' }">
            <td class="nowrap">{{ when(e.lastSeen) }}</td>
            <td class="nowrap">{{ e.object.kind }}/{{ e.object.name }}</td>
            <td>{{ e.reason }}</td>
            <td class="msg">{{ e.message }}</td>
            <td>{{ e.count }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script lang="ts" setup>
import type { IncidentEvent } from '../../types/incident'

defineProps<{ events: IncidentEvent[] }>()

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}
</script>

<style scoped>
.scroll { overflow-x: auto; }
.tbl { width: 100%; border-collapse: collapse; font-size: 12px; }
.tbl th { text-align: left; padding: 6px 10px; color: #a6adc8; border-bottom: 1px solid #313244; }
.tbl td { padding: 6px 10px; border-bottom: 1px solid #232334; vertical-align: top; }
.tbl tr.warn td:nth-child(3) { color: #f9e2af; }
.nowrap { white-space: nowrap; }
.msg { word-break: break-word; }
.muted { color: #6c7086; font-size: 13px; }
</style>
