<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <article v-if="problem" class="problem" data-testid="problem-view" :data-id="problem.id">
    <header class="head">
      <div>
        <h2 data-testid="problem-title">{{ kindText }}: {{ source }}</h2>
        <p class="sub">
          {{ $t(`incidentsIntel.problem.status.${problem.status}`) }} ·
          {{ $t(`incidentsIntel.problem.origin.${problem.origin}`) }} ·
          {{ $t('incidentsIntel.problem.members', { n: problem.memberCount }) }}
        </p>
      </div>
      <button class="btn btn-secondary" type="button" data-testid="problem-back" @click="emit('back')">{{ $t('incidents.back') }}</button>
    </header>

    <section class="block" data-testid="problem-affected">
      <h3>{{ $t('incidentsIntel.problem.affected', { total: problem.affectedTotal }) }}</h3>
      <ul v-if="problem.affected?.length" class="wl">
        <li v-for="(w, i) in problem.affected" :key="i">{{ [w.kind, w.namespace, w.name].filter(Boolean).join('/') }}</li>
      </ul>
      <p v-else class="muted">{{ $t('incidentsIntel.correlation.noneListed') }}</p>
      <p v-if="problem.affectedTruncated" class="warn" data-testid="problem-truncated">
        {{ $t('incidentsIntel.correlation.truncated', { shown: problem.affected?.length ?? 0, total: problem.affectedTotal }) }}
      </p>
    </section>

    <section class="block" data-testid="problem-members">
      <h3>{{ $t('incidentsIntel.problem.membersTitle') }}</h3>
      <ul class="members">
        <li v-for="m in problem.members" :key="m.incidentId" class="member" data-testid="problem-member" :data-id="m.incidentId">
          <button class="link" type="button" data-testid="problem-open-incident" @click="emit('openIncident', m.incidentId)">
            {{ m.summary ? `${m.summary.target.kind}/${m.summary.target.namespace}/${m.summary.target.name}` : m.incidentId }}
          </button>
          <span class="muted"> · {{ m.addedBy === 'auto' ? $t('incidentsIntel.problem.addedAuto') : $t('incidentsIntel.problem.addedBy', { user: m.addedBy }) }}</span>
          <button v-if="canOperate" class="btn btn-secondary" type="button" :disabled="busy" data-testid="problem-detach" @click="emit('detach', m.incidentId)">
            {{ $t('incidentsIntel.problem.detach') }}
          </button>
        </li>
      </ul>
      <p v-if="problem.detached?.length" class="muted" data-testid="problem-detached">
        {{ $t('incidentsIntel.problem.detachedNote', { n: problem.detached.length }) }}
      </p>
    </section>

    <section v-if="canOperate" class="block" data-testid="problem-actions">
      <h3>{{ $t('incidentsIntel.problem.actions') }}</h3>
      <p class="muted" data-testid="problem-actions-note">{{ $t('incidentsIntel.problem.safeNote') }}</p>
      <div class="row">
        <select v-model="attachId" :aria-label="$t('incidentsIntel.problem.attach')" data-testid="problem-attach-select">
          <option value="">{{ $t('incidentsIntel.problem.pickIncident') }}</option>
          <option v-for="i in attachable" :key="i.id" :value="i.id">{{ i.target.kind }}/{{ i.target.namespace }}/{{ i.target.name }}</option>
        </select>
        <button class="btn btn-secondary" type="button" :disabled="busy || !attachId" data-testid="problem-attach" @click="emit('attach', attachId)">
          {{ $t('incidentsIntel.problem.attach') }}
        </button>
      </div>
      <div class="row">
        <select v-model="mergeId" :aria-label="$t('incidentsIntel.problem.merge')" data-testid="problem-merge-select">
          <option value="">{{ $t('incidentsIntel.problem.pickProblem') }}</option>
          <option v-for="p in mergeTargets" :key="p.id" :value="p.id">{{ p.source.kind }}/{{ p.source.name }} ({{ p.memberCount }})</option>
        </select>
        <button class="btn btn-secondary" type="button" :disabled="busy || !mergeId" data-testid="problem-merge" @click="emit('merge', mergeId)">
          {{ $t('incidentsIntel.problem.merge') }}
        </button>
      </div>
    </section>
    <p v-else class="muted" data-testid="problem-readonly">{{ $t('incidentsIntel.problem.needOperate') }}</p>
  </article>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import type { IncidentSummary, ProblemDetail, ProblemSummary } from '../../types/incident'

/**
 * Экран проблемы: участники (ссылки на расследования), затронутое множество, ручные действия.
 * Действия меняют только группировку; без права `operate` кнопки не показываются.
 */
const props = withDefaults(defineProps<{
  problem?: ProblemDetail | null
  canOperate?: boolean
  busy?: boolean
  attachable?: IncidentSummary[]
  mergeTargets?: ProblemSummary[]
}>(), { problem: null, canOperate: false, busy: false, attachable: () => [], mergeTargets: () => [] })
const emit = defineEmits<{
  (e: 'back'): void
  (e: 'openIncident', id: string): void
  (e: 'attach', incidentId: string): void
  (e: 'detach', incidentId: string): void
  (e: 'merge', toId: string): void
}>()
const { t } = useI18n()
const attachId = ref('')
const mergeId = ref('')

const source = computed(() => (props.problem ? [props.problem.source.kind, props.problem.source.namespace, props.problem.source.name].filter(Boolean).join('/') : ''))
const kindText = computed(() => {
  const key = `incidentsCorrelation.kinds.${props.problem?.kind ?? ''}`
  return te(key) ? t(key) : t('incidentsIntel.correlation.kindOther')
})
</script>

<style scoped>
.problem { display: flex; flex-direction: column; gap: 16px; }
.head { display: flex; justify-content: space-between; gap: 16px; }
.head h2 { margin: 0; font-size: 20px; color: #89b4fa; }
.sub, .muted { margin: 4px 0 0; color: #a6adc8; font-size: 12px; }
.block h3 { margin: 0 0 8px; font-size: 15px; color: #cdd6f4; }
.wl, .members { margin: 0; padding-left: 18px; font-size: 13px; color: #cdd6f4; }
.members { list-style: none; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.member { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.link { background: none; border: 0; padding: 0; color: #89b4fa; cursor: pointer; text-decoration: underline; font: inherit; }
.warn { margin: 4px 0 0; color: #fab387; font-size: 12px; }
.row { display: flex; gap: 8px; margin-top: 8px; }
</style>
