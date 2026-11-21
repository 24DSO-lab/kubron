<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="stats" data-testid="rule-stats">
    <p v-if="!stats" class="muted" data-testid="stats-empty">{{ loading ? $t('incidentsInsights.stats.loading') : $t('incidentsInsights.stats.none') }}</p>
    <template v-else>
      <p class="muted" data-testid="stats-scope">{{ $t('incidentsInsights.stats.scope', { records: stats.records, unevaluated: stats.unevaluated }) }}</p>
      <p class="muted" data-testid="stats-note">{{ $t('incidentsInsights.stats.note') }}</p>
      <p v-if="stats.truncated" class="warn" data-testid="stats-truncated">{{ $t('incidentsInsights.stats.truncated') }}</p>

      <p v-if="!stats.hypotheses.length && !stats.recommendations.length && !stats.symptoms.length" class="muted" data-testid="stats-nothing">
        {{ $t('incidentsInsights.stats.nothing') }}
      </p>

      <template v-if="stats.hypotheses.length">
        <h4>{{ $t('incidentsInsights.stats.hypotheses') }}</h4>
        <table class="tbl" data-testid="stats-hypotheses">
          <thead><tr>
            <th>{{ $t('incidentsInsights.stats.cols.rule') }}</th><th>{{ $t('incidentsInsights.stats.cols.confirmed') }}</th>
            <th>{{ $t('incidentsInsights.stats.cols.rejected') }}</th><th>{{ $t('incidentsInsights.stats.cols.share') }}</th>
            <th>{{ $t('incidentsInsights.stats.cols.time') }}</th><th>{{ $t('incidentsInsights.stats.cols.recurred') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="h in stats.hypotheses" :key="`${h.ruleId}|${h.epoch}|${h.code}`" data-testid="stats-hypothesis" :data-trusted="h.trusted">
              <td>{{ codeText('hypotheses', h.code) }}</td><td>{{ h.confirmed }}</td><td>{{ h.rejected }}</td>
              <td data-testid="stats-share">{{ share(h.precision, h.trusted) }}</td>
              <td>{{ formatDuration(h.medianResolveSeconds) || '—' }}</td>
              <td>{{ h.recurred }}/{{ h.resolved }}</td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-if="stats.recommendations.length">
        <h4>{{ $t('incidentsInsights.stats.recommendations') }}</h4>
        <table class="tbl" data-testid="stats-recommendations">
          <thead><tr>
            <th>{{ $t('incidentsInsights.stats.cols.rule') }}</th><th>{{ $t('incidentsInsights.stats.cols.helpful') }}</th>
            <th>{{ $t('incidentsInsights.stats.cols.useless') }}</th><th>{{ $t('incidentsInsights.stats.cols.share') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="r in stats.recommendations" :key="`${r.ruleId}|${r.epoch}|${r.code}`" data-testid="stats-recommendation" :data-trusted="r.trusted">
              <td>
                {{ codeText('recommendations', r.code) }}
                <span v-if="r.previouslyUnhelpful" class="warn" data-testid="stats-prev-unhelpful">{{ $t('incidentsInsights.stats.previouslyUnhelpfulShort') }}</span>
              </td>
              <td>{{ r.helpful }}</td><td>{{ r.useless }}</td>
              <td data-testid="stats-share">{{ share(r.ratio, r.trusted) }}</td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-if="stats.symptoms.length">
        <h4>{{ $t('incidentsInsights.stats.symptoms') }}</h4>
        <table class="tbl" data-testid="stats-symptoms">
          <thead><tr>
            <th>{{ $t('incidentsInsights.stats.cols.symptom') }}</th><th>{{ $t('incidentsInsights.stats.cols.closed') }}</th>
            <th>{{ $t('incidentsInsights.stats.cols.withConfirmed') }}</th><th>{{ $t('incidentsInsights.stats.cols.coverage') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="s in stats.symptoms" :key="s.code" data-testid="stats-symptom">
              <td>{{ codeText('symptoms', s.code) }}</td><td>{{ s.closed }}</td><td>{{ s.withConfirmed }}</td>
              <td data-testid="stats-share">{{ share(s.coverage, s.closed >= MIN_SAMPLE) }}</td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-if="stats.correlations.length">
        <h4>{{ $t('incidentsInsights.stats.correlations') }}</h4>
        <table class="tbl" data-testid="stats-correlations">
          <thead><tr>
            <th>{{ $t('incidentsInsights.stats.cols.kind') }}</th><th>{{ $t('incidentsInsights.stats.cols.confirmed') }}</th>
            <th>{{ $t('incidentsInsights.stats.cols.rejected') }}</th><th>{{ $t('incidentsInsights.stats.cols.share') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="c in stats.correlations" :key="`${c.kind}|${c.confidence}`" data-testid="stats-correlation">
              <td>{{ kindText(c.kind) }} ({{ $t(`incidents.confidence.${confidenceLevel(c.confidence)}`) }})</td>
              <td>{{ c.confirmed }}</td><td>{{ c.rejected }}</td>
              <td data-testid="stats-share">{{ share(c.precision, c.confirmed + c.rejected >= MIN_SAMPLE) }}</td>
            </tr>
          </tbody>
        </table>
      </template>
    </template>

    <div v-if="canExport" class="export" data-testid="stats-export">
      <h4>{{ $t('incidentsInsights.stats.exportTitle') }}</h4>
      <p class="muted" data-testid="stats-export-info">{{ $t('incidentsInsights.stats.exportInfo') }}</p>
      <button class="btn btn-secondary" type="button" :disabled="busy" data-testid="stats-export-button" @click="emit('export')">
        {{ $t('incidentsInsights.stats.exportButton') }}
      </button>
    </div>
  </section>
</template>

<script lang="ts" setup>
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import { codeText, confidenceLevel } from '../../composables/incidentModel'
import { formatDuration, percent } from '../../composables/ruleStatsView'
import type { RuleStats } from '../../types/incident'

/**
 * Статистика правил этого кластера (ADR-0034 §6–8): коды правил и числа. Доли показываются только при выборке не меньше
 * порога доверия, иначе — прочерк. Выгрузка для мантейнеров — вручную, анонимно, с правом `users.manage`.
 */
withDefaults(defineProps<{ stats?: RuleStats | null; canExport?: boolean; busy?: boolean; loading?: boolean }>(),
  { stats: null, canExport: false, busy: false, loading: false })
const emit = defineEmits<{ (e: 'export'): void }>()
const { t } = useI18n()

/** Порог доверия (internal/feedback.MinSample): не меньше 5 оценённых случаев. */
const MIN_SAMPLE = 5
const share = (v: number | undefined, trusted: boolean) => percent(v, trusted) || '—'
const kindText = (kind: string) => (te(`incidentsCorrelation.kinds.${kind}`) ? t(`incidentsCorrelation.kinds.${kind}`) : t('incidentsIntel.correlation.kindOther'))
</script>

<style scoped>
.stats { display: flex; flex-direction: column; gap: 8px; }
.stats h4 { margin: 10px 0 4px; font-size: 13px; color: #cdd6f4; }
.muted { margin: 0; color: #a6adc8; font-size: 12px; }
.warn { margin: 0; color: #fab387; font-size: 12px; }
.tbl { width: 100%; border-collapse: collapse; font-size: 12px; }
.tbl th, .tbl td { padding: 4px 8px; text-align: left; border-bottom: 1px solid #313244; }
.export { margin-top: 12px; padding-top: 8px; border-top: 1px solid #313244; }
</style>
