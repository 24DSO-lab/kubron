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

    <RecurrenceBanner :incident="incident" :previous="previous" @open-incident="(id) => emit('openIncident', id)" />

    <FindingsPanel :incident="incident" :stats="stats" />

    <section v-if="incident.report.graph && DependencyMap" class="block" data-testid="dependency-map-block">
      <h3>{{ $t('incidents.sections.map') }}</h3>
      <DependencyMap :report="incident.report" :selected="selectedNode" @select="(k: string) => (selectedNode = k)" />
    </section>

    <CorrelationBlock v-if="incident.correlation || incident.problemId" :correlation="incident.correlation" :problem-id="incident.problemId" :status="incident.status" :busy="busy"
      :can-operate="canOperate" @open-problem="(id) => emit('openProblem', id)" @recompute="emit('recomputeCorrelation')" />

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
import { defineAsyncComponent, ref } from 'vue'
import CorrelationBlock from './CorrelationBlock.vue'
import EventsTable from './EventsTable.vue'
import EvidencePanel from './EvidencePanel.vue'
import FindingsPanel from './FindingsPanel.vue'
import OutcomeForm from './OutcomeForm.vue'
import RecurrenceBanner from './RecurrenceBanner.vue'
import type { Evidence, Fragment, FragmentView, Incident, OutcomeInput, RuleStats } from '../../types/incident'

defineProps<{
  incident: Incident
  fragments: FragmentView[]
  canReadLogs: boolean
  read: (id: string) => Promise<Fragment>
  busy?: boolean
  stats?: RuleStats | null
  /** предыдущее расследование при повторении (для баннера); null — недоступно */
  previous?: Incident | null
  /** право operate: подсказка для кнопки пересчёта корреляции */
  canOperate?: boolean
}>()
const emit = defineEmits<{
  (e: 'back'): void
  (e: 'openProblem', id: string): void
  (e: 'openIncident', id: string): void
  (e: 'refresh'): void
  (e: 'recomputeCorrelation'): void
  (e: 'export', withFragments: boolean): void
  (e: 'delete'): void
  (e: 'saveFragment', ev: Evidence): void
  (e: 'deleteFragment', id: string): void
  (e: 'deleteAllFragments'): void
  (e: 'capture', enabled: boolean): void
  (e: 'outcome', v: OutcomeInput): void
}>()

/**
 * Карта зависимостей D7 подключается лениво и только если файл есть в сборке: пока карты нет, раздел не показывается,
 * а сборка не падает. Выбранный узел хранится здесь, чтобы переживать перерисовку отчёта.
 */
const MAP_PATH = './map/DependencyMap.vue'
const mapLoaders = import.meta.glob('./map/DependencyMap.vue')
const DependencyMap = mapLoaders[MAP_PATH] ? defineAsyncComponent(mapLoaders[MAP_PATH] as () => Promise<never>) : null
const selectedNode = ref('')

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
