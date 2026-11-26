<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="outcome" data-testid="outcome">
    <div v-if="incident.outcome" class="done" data-testid="outcome-done">
      <p class="result">{{ $t('incidents.outcome.done', { result: $t(`incidents.outcome.results.${incident.outcome.result}`) }) }}</p>
      <p class="by">{{ $t('incidents.outcome.by', { user: incident.outcome.markedBy, time: when(incident.outcome.markedAt) }) }}</p>
      <ul v-if="marks.length" class="marks" data-testid="outcome-marks">
        <li v-for="(m, i) in marks" :key="i">{{ m }}</li>
      </ul>
      <p v-if="incident.outcome.note" class="note" data-testid="outcome-note">{{ incident.outcome.note }}</p>
    </div>
    <form v-else class="form" @submit.prevent="submit">
      <label class="field">
        <span>{{ $t('incidents.outcome.result') }}</span>
        <select v-model="state.result" class="form-input" data-testid="outcome-result">
          <option v-for="r in RESULTS" :key="r" :value="r">{{ $t(`incidents.outcome.results.${r}`) }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ $t('incidents.outcome.confirmed') }}</span>
        <select v-model="state.confirmed" class="form-input" data-testid="outcome-hypothesis">
          <option value="">{{ $t('incidents.outcome.noneConfirmed') }}</option>
          <option v-for="h in incident.report.hypotheses" :key="h.id" :value="h.id">{{ codeText('hypotheses', h.code, h.params) }}</option>
        </select>
      </label>

      <details class="more" data-testid="outcome-more">
        <summary>{{ $t('incidentsIntel.outcome.more') }}</summary>
        <p class="hint" data-testid="outcome-optional-hint">{{ $t('incidentsIntel.outcome.optionalHint') }}</p>

        <fieldset v-if="rejectable.length" class="group" data-testid="outcome-rejected">
          <legend>{{ $t('incidentsIntel.outcome.rejected') }}</legend>
          <label v-for="h in rejectable" :key="h.id" class="check">
            <input v-model="state.rejected" type="checkbox" :value="h.id" data-testid="outcome-reject" />
            {{ codeText('hypotheses', h.code, h.params) }}
          </label>
        </fieldset>

        <fieldset v-if="incident.report.recommendations.length" class="group" data-testid="outcome-recommendations">
          <legend>{{ $t('incidentsIntel.outcome.recommendations') }}</legend>
          <div v-for="r in incident.report.recommendations" :key="r.id" class="rec" data-testid="outcome-rec" :data-id="r.id">
            <span class="rec-text">{{ codeText('recommendations', r.code, r.params) }}</span>
            <span class="rec-marks" role="radiogroup" :aria-label="codeText('recommendations', r.code, r.params)">
              <label v-for="m in MARKS" :key="m" class="check">
                <input v-model="state.recs[r.id]" type="radio" :name="`rec-${r.id}`" :value="m" :data-testid="`outcome-rec-${m}`" />
                {{ $t(`incidentsIntel.outcome.mark.${m}`) }}
              </label>
            </span>
          </div>
        </fieldset>

        <template v-if="state.result === 'resolved'">
          <label class="check" data-testid="outcome-other-action">
            <input v-model="state.otherAction" type="checkbox" data-testid="outcome-other-action-input" />
            {{ $t('incidentsIntel.outcome.otherAction') }}
          </label>
          <label class="field">
            <span>{{ $t('incidentsIntel.outcome.resolvedAt') }}</span>
            <input v-model="state.resolvedAt" type="datetime-local" class="form-input" :min="minAt" :max="maxAt" data-testid="outcome-resolved-at" />
            <span class="hint">{{ $t('incidentsIntel.outcome.resolvedAtHint') }}</span>
          </label>
        </template>

        <fieldset v-if="candidates.length" class="group" data-testid="outcome-correlation">
          <legend>{{ $t('incidentsIntel.outcome.correlation') }}</legend>
          <select v-if="candidates.length > 1" v-model="state.correlationCandidate" class="form-input" data-testid="outcome-correlation-candidate"
            :aria-label="$t('incidentsIntel.outcome.correlation')">
            <option v-for="c in candidates" :key="c.id" :value="c.id">{{ c.label }}</option>
          </select>
          <p v-else class="hint">{{ candidates[0].label }}</p>
          <span role="radiogroup" class="rec-marks">
            <label v-for="v in VERDICTS" :key="v || 'none'" class="check">
              <input v-model="state.correlationVerdict" type="radio" name="corr-verdict" :value="v" :data-testid="`outcome-verdict-${v || 'none'}`" />
              {{ $t(`incidentsIntel.outcome.verdict.${v || 'none'}`) }}
            </label>
          </span>
        </fieldset>
      </details>

      <label class="field">
        <span>{{ $t('incidents.outcome.note') }}</span>
        <textarea v-model="state.note" class="form-input" rows="3" :maxlength="MAX_NOTE_LENGTH" data-testid="outcome-note-input"></textarea>
      </label>

      <p v-if="error" class="form-error" role="alert" data-testid="outcome-error">{{ $t(`incidentsIntel.outcome.errors.${error}`) }}</p>
      <button class="btn" type="submit" :disabled="busy" data-testid="outcome-submit">{{ $t('incidents.outcome.submit') }}</button>
    </form>
  </div>
</template>

<script lang="ts" setup>
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import { codeText } from '../../composables/incidentModel'
import { MAX_NOTE_LENGTH, buildOutcomeInput, emptyOutcomeState, toLocalInput, validateOutcome, type OutcomeError, type RecMark } from '../../composables/outcomeInput'
import type { CorrelationVerdictValue, Incident, OutcomeInput, OutcomeResult } from '../../types/incident'

/**
 * Отметка результата (ADR-0034 §2). Обязателен только результат; остальное — необязательные отметки напротив
 * гипотез и рекомендаций (по умолчанию «не отмечено»): без них исход сохраняется, и статистика не получает ложных «не помогло».
 */
const props = defineProps<{ incident: Incident; busy?: boolean }>()
const emit = defineEmits<{ (e: 'submit', v: OutcomeInput): void }>()
const { t } = useI18n()

const RESULTS: OutcomeResult[] = ['resolved', 'unresolved', 'inconclusive']
const MARKS: RecMark[] = ['none', 'helpful', 'useless']
const VERDICTS: (CorrelationVerdictValue | '')[] = ['', 'confirmed', 'rejected', 'unknown']

const state = reactive(emptyOutcomeState())
const error = ref<OutcomeError>('')

// «Не отмечено» выбрано по умолчанию у каждой рекомендации; в запрос такие не попадают (см. buildOutcomeInput).
watch(() => props.incident.report.recommendations, (recs) => {
  for (const r of recs) if (!(r.id in state.recs)) state.recs[r.id] = 'none'
}, { immediate: true })

const candidates = computed(() =>
  (props.incident.correlation?.candidates ?? []).map((c) => {
    const kindKey = `incidentsCorrelation.kinds.${c.kind}`
    const src = [c.source.kind, c.source.namespace, c.source.name].filter(Boolean).join('/')
    return { id: c.id, label: `${te(kindKey) ? t(kindKey) : t('incidentsIntel.correlation.kindOther')}: ${src}` }
  }),
)
watch(candidates, (c) => { if (!state.correlationCandidate && c.length) state.correlationCandidate = c[0].id }, { immediate: true })

/** Подтверждённая гипотеза не предлагается к отклонению. */
const rejectable = computed(() => props.incident.report.hypotheses.filter((h) => h.id !== state.confirmed))
watch(() => state.confirmed, (id) => { state.rejected = state.rejected.filter((r) => r !== id) })

const minAt = computed(() => toLocalInput(props.incident.openedAt))
const maxAt = computed(() => toLocalInput(new Date().toISOString()))

/** Сводка уже сохранённой отметки: что человек отметил, словами. */
const marks = computed(() => {
  const o = props.incident.outcome
  if (!o) return []
  const hyp = (id: string) => {
    const h = props.incident.report.hypotheses.find((x) => x.id === id)
    return h ? codeText('hypotheses', h.code, h.params) : ''
  }
  const rec = (id: string) => {
    const r = props.incident.report.recommendations.find((x) => x.id === id)
    return r ? codeText('recommendations', r.code, r.params) : ''
  }
  const list = (ids: string[] | undefined, f: (id: string) => string) => (ids ?? []).map(f).filter(Boolean).join('; ')
  const out: string[] = []
  const rejected = list(o.rejectedHypothesisIds, hyp)
  if (rejected) out.push(t('incidentsIntel.outcome.doneRejected', { items: rejected }))
  const helpful = list(o.helpfulRecommendationIds, rec)
  if (helpful) out.push(t('incidentsIntel.outcome.doneHelpful', { items: helpful }))
  const useless = list(o.uselessRecommendationIds, rec)
  if (useless) out.push(t('incidentsIntel.outcome.doneUseless', { items: useless }))
  if (o.otherAction) out.push(t('incidentsIntel.outcome.doneOtherAction'))
  if (o.resolvedAt) out.push(t('incidentsIntel.outcome.doneResolvedAt', { time: when(o.resolvedAt) }))
  if (o.correlation) out.push(t('incidentsIntel.outcome.doneCorrelation', { verdict: t(`incidentsIntel.outcome.verdict.${o.correlation.verdict}`) }))
  return out
})

function submit() {
  error.value = validateOutcome(state, { openedAt: props.incident.openedAt })
  if (error.value) return
  emit('submit', buildOutcomeInput(state))
}

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<style scoped>
.form { display: flex; flex-direction: column; gap: 10px; max-width: 560px; }
.field { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: #a6adc8; }
.form-input { padding: 8px 10px; }
.more { border: 1px solid #313244; border-radius: 8px; padding: 8px 12px; }
.more summary { cursor: pointer; color: #cdd6f4; font-size: 13px; }
.hint { margin: 6px 0; color: #a6adc8; font-size: 12px; }
.group { border: 0; margin: 8px 0 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.group legend { padding: 0; margin-bottom: 4px; font-size: 13px; color: #cdd6f4; }
.check { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; color: #cdd6f4; }
.rec { display: flex; flex-direction: column; gap: 2px; padding: 4px 0; }
.rec-text { font-size: 13px; color: #cdd6f4; }
.rec-marks { display: flex; flex-wrap: wrap; gap: 12px; }
.result { margin: 0; font-weight: 600; color: #a6e3a1; }
.by { margin: 2px 0 0; font-size: 12px; color: #a6adc8; }
.marks { margin: 6px 0 0; padding-left: 18px; font-size: 13px; color: #cdd6f4; }
.note { margin: 8px 0 0; white-space: pre-wrap; color: #cdd6f4; font-size: 13px; }
</style>
