<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <aside v-if="incident.recurrenceOf" class="recur" data-testid="recurrence-banner" role="note">
    <h3>{{ $t('incidentsInsights.recurrence.title') }}</h3>
    <template v-if="previous">
      <p class="text" data-testid="recurrence-summary">{{ summary }}</p>
      <p v-if="confirmed" class="text" data-testid="recurrence-confirmed">{{ $t('incidentsInsights.recurrence.confirmed', { cause: confirmed }) }}</p>
      <p v-if="helped" class="text" data-testid="recurrence-helped">{{ $t('incidentsInsights.recurrence.helped', { items: helped }) }}</p>
    </template>
    <p v-else class="text" data-testid="recurrence-unavailable">{{ $t('incidentsInsights.recurrence.unavailable') }}</p>
    <button v-if="previous" class="btn btn-secondary" type="button" data-testid="recurrence-open" @click="emit('openIncident', previous.id)">
      {{ $t('incidentsInsights.recurrence.open') }}
    </button>
  </aside>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { codeText } from '../../composables/incidentModel'
import type { Incident } from '../../types/incident'

/**
 * Баннер повторения (ADR-0034 §5): «похоже на повторение», что было раньше и что помогло. Только факты прошлого
 * расследования; если оно недоступно (удалено, нет прав) — баннер без подробностей.
 */
const props = defineProps<{ incident: Incident; previous?: Incident | null }>()
const emit = defineEmits<{ (e: 'openIncident', id: string): void }>()
const { t } = useI18n()

const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

const summary = computed(() => {
  const o = props.previous?.outcome
  if (!o) return t('incidentsInsights.recurrence.summaryNoOutcome', { time: when(props.previous?.openedAt ?? '') })
  return t('incidentsInsights.recurrence.summary', { time: when(o.resolvedAt ?? o.markedAt), result: t(`incidents.outcome.results.${o.result}`) })
})

const confirmed = computed(() => {
  const p = props.previous
  const h = p?.report.hypotheses.find((x) => x.id === p.outcome?.confirmedHypothesisId)
  return h ? codeText('hypotheses', h.code, h.params) : ''
})

const helped = computed(() => {
  const p = props.previous
  return (p?.outcome?.helpfulRecommendationIds ?? [])
    .map((id) => p!.report.recommendations.find((r) => r.id === id))
    .filter((r): r is NonNullable<typeof r> => !!r)
    .map((r) => codeText('recommendations', r.code, r.params))
    .join('; ')
})
</script>

<style scoped>
.recur { padding: 10px 12px; border-radius: 8px; border: 1px solid #45475a; background: rgba(250, 179, 135, 0.06); }
.recur h3 { margin: 0 0 6px; font-size: 14px; color: #fab387; }
.text { margin: 0 0 6px; font-size: 13px; color: #cdd6f4; }
</style>
