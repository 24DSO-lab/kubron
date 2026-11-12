<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <article class="detail" data-testid="incident-detail" :data-id="incident.id">
    <header class="head">
      <div>
        <h2 data-testid="detail-title">{{ incident.target.kind }}/{{ incident.target.namespace }}/{{ incident.target.name }}</h2>
        <p class="sub">
          <span class="badge" :class="`st-${incident.status}`" data-testid="detail-status">{{ $t(`incidents.status.${incident.status}`) }}</span>
          {{ $t('incidents.detail.opened', { time: when(incident.openedAt), user: incident.openedBy }) }} ·
          {{ $t('incidents.detail.ruleSet', { version: incident.report.ruleSetVersion }) }}
        </p>
      </div>
      <div class="actions">
        <button class="btn btn-secondary" type="button" data-testid="back" @click="emit('back')">{{ $t('incidents.back') }}</button>
        <button v-if="incident.status === 'open'" class="btn btn-secondary" type="button" :disabled="busy" data-testid="refresh-events"
          @click="emit('refresh')">{{ $t('incidents.detail.refreshEvents') }}</button>
        <button class="btn btn-secondary" type="button" :disabled="busy" data-testid="export" @click="emit('export', false)">
          {{ $t('incidents.detail.export') }}
        </button>
        <button v-if="canReadLogs && fragments.length" class="btn btn-secondary" type="button" :disabled="busy" data-testid="export-with-logs"
          :title="$t('incidents.detail.exportFragments')" @click="emit('export', true)">
          {{ $t('incidents.detail.exportFragments') }}
        </button>
        <button v-if="canReadLogs" class="btn btn-danger" type="button" :disabled="busy" data-testid="delete-incident" @click="emit('delete')">
          {{ $t('incidents.detail.delete') }}
        </button>
      </div>
    </header>

    <FindingsPanel :incident="incident" />

    <section class="block">
      <h3>{{ $t('incidents.sections.events') }}</h3>
      <p class="sub" data-testid="events-updated">{{ $t('incidents.detail.eventsUpdated', { time: when(incident.eventsUpdatedAt) }) }}</p>
      <EventsTable :events="incident.events" />
    </section>

    <section class="block">
      <h3>{{ $t('incidents.sections.evidence') }}</h3>
      <EvidencePanel :incident="incident" :fragments="fragments" :read="read" :can-delete="canReadLogs" :busy="busy"
        @save="(ev) => emit('saveFragment', ev)" @delete="(id) => emit('deleteFragment', id)"
        @delete-all="emit('deleteAllFragments')" @capture="(on) => emit('capture', on)" />
    </section>

    <section class="block">
      <h3>{{ $t('incidents.sections.outcome') }}</h3>
      <OutcomeForm :incident="incident" :busy="busy" @submit="(v) => emit('outcome', v)" />
    </section>
  </article>
</template>

<script lang="ts" setup>
import EventsTable from './EventsTable.vue'
import EvidencePanel from './EvidencePanel.vue'
import FindingsPanel from './FindingsPanel.vue'
import OutcomeForm from './OutcomeForm.vue'
import type { Evidence, Fragment, FragmentView, Incident, OutcomeResult } from '../../types/incident'

defineProps<{
  incident: Incident
  fragments: FragmentView[]
  canReadLogs: boolean
  read: (id: string) => Promise<Fragment>
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'back'): void
  (e: 'refresh'): void
  (e: 'export', withFragments: boolean): void
  (e: 'delete'): void
  (e: 'saveFragment', ev: Evidence): void
  (e: 'deleteFragment', id: string): void
  (e: 'deleteAllFragments'): void
  (e: 'capture', enabled: boolean): void
  (e: 'outcome', v: { result: OutcomeResult; confirmed: string; note: string }): void
}>()

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<style scoped>
.detail { display: flex; flex-direction: column; gap: 22px; }
.head { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; flex-wrap: wrap; }
.head h2 { margin: 0; font-size: 20px; color: #89b4fa; }
.sub { margin: 4px 0 0; color: #a6adc8; font-size: 12px; }
.actions { display: flex; gap: 8px; flex-wrap: wrap; }
.block h3 { margin: 0 0 8px; font-size: 15px; color: #cdd6f4; }
.badge { padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; margin-right: 6px; }
.st-open { background: rgba(249, 226, 175, 0.15); color: #f9e2af; }
.st-closed { background: rgba(166, 227, 161, 0.15); color: #a6e3a1; }
</style>
