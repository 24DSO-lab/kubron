<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="problems" data-testid="problem-list">
    <div class="filters" role="group" :aria-label="$t('incidentsIntel.problem.title')">
      <button v-for="f in FILTERS" :key="f" type="button" class="btn btn-secondary" :class="{ active: status === f }"
        :data-testid="`problem-filter-${f || 'all'}`" @click="emit('filter', f)">
        {{ $t(`incidentsIntel.problem.filter.${f || 'all'}`) }}
      </button>
    </div>

    <p v-if="!items.length && !loading" class="muted" data-testid="problem-empty">{{ $t('incidentsIntel.problem.empty') }}</p>
    <table v-else class="tbl">
      <thead>
        <tr>
          <th>{{ $t('incidentsIntel.problem.columns.source') }}</th>
          <th>{{ $t('incidentsIntel.problem.columns.members') }}</th>
          <th>{{ $t('incidentsIntel.problem.columns.affected') }}</th>
          <th>{{ $t('incidentsIntel.problem.columns.updated') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="p in items" :key="p.id" class="row" data-testid="problem-row" :data-id="p.id" tabindex="0"
          @click="emit('select', p.id)" @keydown.enter="emit('select', p.id)">
          <td>{{ kindText(p.kind) }}: {{ source(p) }}<span v-if="p.origin === 'manual'" class="badge" data-testid="problem-manual">{{ $t('incidentsIntel.problem.manual') }}</span></td>
          <td>{{ p.memberCount }}</td>
          <td>{{ p.affectedTotal }}</td>
          <td>{{ when(p.updatedAt) }}</td>
        </tr>
      </tbody>
    </table>
    <button v-if="hasMore" type="button" class="btn btn-secondary" :disabled="loading" data-testid="problem-more" @click="emit('more')">
      {{ $t('incidents.list.more') }}
    </button>

    <section v-if="canOperate" class="create" data-testid="problem-create">
      <h4>{{ $t('incidentsIntel.problem.createTitle') }}</h4>
      <p class="muted">{{ $t('incidentsIntel.problem.createHint') }}</p>
      <p v-if="!candidates.length" class="muted" data-testid="problem-create-empty">{{ $t('incidentsIntel.problem.createEmpty') }}</p>
      <ul v-else class="picks">
        <li v-for="i in candidates" :key="i.id">
          <label>
            <input v-model="picked" type="checkbox" :value="i.id" data-testid="problem-pick" />
            {{ i.target.kind }}/{{ i.target.namespace }}/{{ i.target.name }}
          </label>
        </li>
      </ul>
      <button type="button" class="btn btn-secondary" :disabled="busy || !picked.length" data-testid="problem-create-submit"
        @click="emit('create', [...picked])">
        {{ $t('incidentsIntel.problem.createSubmit', { n: picked.length }) }}
      </button>
    </section>
  </div>
</template>

<script lang="ts" setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import type { IncidentSummary, ProblemStatus, ProblemSummary } from '../../types/incident'

/** Открытые проблемы: источник, участники, затронуто. Создание из выбранных расследований — только с правом `operate`. */
withDefaults(defineProps<{
  items?: ProblemSummary[]
  status?: ProblemStatus | ''
  loading?: boolean
  hasMore?: boolean
  canOperate?: boolean
  busy?: boolean
  /** Расследования, которые можно объединить в новую проблему (ещё не в проблеме) */
  candidates?: IncidentSummary[]
}>(), { items: () => [], status: 'open', loading: false, hasMore: false, canOperate: false, busy: false, candidates: () => [] })
const emit = defineEmits<{
  (e: 'select', id: string): void
  (e: 'filter', s: ProblemStatus | ''): void
  (e: 'more'): void
  (e: 'create', ids: string[]): void
}>()
const { t } = useI18n()
const FILTERS: (ProblemStatus | '')[] = ['open', 'closed', '']
const picked = ref<string[]>([])

const source = (p: ProblemSummary) => [p.source.kind, p.source.namespace, p.source.name].filter(Boolean).join('/')
const kindText = (kind: string) => (te(`incidentsCorrelation.kinds.${kind}`) ? t(`incidentsCorrelation.kinds.${kind}`) : t('incidentsIntel.correlation.kindOther'))
const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<style scoped>
.problems { display: flex; flex-direction: column; gap: 10px; }
.filters { display: flex; gap: 6px; }
.filters .active { border-color: #89b4fa; }
.tbl { width: 100%; border-collapse: collapse; font-size: 13px; }
.tbl th, .tbl td { padding: 6px 8px; text-align: left; border-bottom: 1px solid #313244; }
.row { cursor: pointer; }
.row:focus-visible { outline: 2px solid #89b4fa; }
.badge { margin-left: 8px; padding: 0 6px; border-radius: 4px; background: #313244; font-size: 11px; }
.muted { margin: 0; color: #a6adc8; font-size: 12px; }
.picks { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
.create h4 { margin: 0 0 4px; font-size: 14px; color: #cdd6f4; }
</style>
