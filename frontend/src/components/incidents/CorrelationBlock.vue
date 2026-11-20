<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section v-if="candidates.length || notes.length" class="corr" data-testid="correlation-block" :aria-label="$t('incidentsCorrelation.title')">
    <h3>{{ $t('incidentsCorrelation.title') }}</h3>
    <p v-if="candidates.length" class="note" data-testid="correlation-note">{{ $t('incidentsIntel.correlation.note') }}</p>

    <article v-for="c in candidates" :key="c.id" class="cand" data-testid="correlation-candidate" :data-confidence="c.confidence" :data-kind="c.kind">
      <h4>{{ c.kindText }}: <span class="src" data-testid="correlation-source">{{ c.source }}</span></h4>
      <p class="meta">
        <span class="conf" :class="`conf-${c.confidence}`" data-testid="correlation-confidence">
          {{ $t('incidents.confidence.label', { level: $t(`incidents.confidence.${c.confidence}`) }) }}
        </span>
        <span v-if="c.since"> · {{ $t('incidentsIntel.correlation.since', { time: c.since }) }}</span>
      </p>
      <p v-if="c.confidence === 'low'" class="hint" data-testid="correlation-low">{{ $t('incidentsIntel.correlation.lowHint') }}</p>

      <div class="group" data-testid="correlation-affected">
        <h5>{{ $t('incidentsIntel.correlation.affected', { total: c.total }) }}</h5>
        <ul v-if="c.affected.length" class="wl">
          <li v-for="(w, i) in c.affected" :key="i" data-testid="correlation-workload">
            {{ w.label }}<span v-if="w.detail" class="muted"> — {{ w.detail }}</span>
          </li>
        </ul>
        <p v-else class="muted">{{ $t('incidentsIntel.correlation.noneListed') }}</p>
        <p v-if="c.hidden > 0" class="warn" data-testid="correlation-truncated">
          {{ $t('incidentsIntel.correlation.truncated', { shown: c.affected.length, total: c.total }) }}
        </p>
      </div>

      <div v-if="c.potential.length" class="group" data-testid="correlation-potential">
        <h5>{{ $t('incidentsIntel.correlation.potential') }}</h5>
        <p class="muted">{{ $t('incidentsIntel.correlation.potentialNote') }}</p>
        <ul class="wl">
          <li v-for="(w, i) in c.potential" :key="i">{{ w.label }}</li>
        </ul>
      </div>

      <div v-if="c.facts.length" class="group" data-testid="correlation-facts">
        <h5>{{ $t('incidentsIntel.correlation.supporting') }}</h5>
        <ul class="wl"><li v-for="f in c.facts" :key="f.id">{{ f.text }}</li></ul>
      </div>
    </article>

    <ul v-if="notes.length" class="notes" data-testid="correlation-notes">
      <li v-for="(n, i) in notes" :key="i" class="warn" data-testid="correlation-incomplete">{{ n }}</li>
    </ul>

    <button v-if="problemId" class="btn btn-secondary" type="button" data-testid="correlation-open-problem" @click="emit('openProblem', problemId)">
      {{ $t('incidentsIntel.correlation.openProblem') }}
    </button>
  </section>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import { codeText, confidenceLevel, factsByIds } from '../../composables/incidentModel'
import type { CorrelationResult, ResourceRef, WorkloadRef } from '../../types/incident'

/**
 * «Возможный общий источник» (ADR-0033 §5): вероятный, а не установленный. Затронутые нагрузки и область
 * потенциального влияния показываются раздельно; усечение и нехватка прав названы явно.
 */
const props = defineProps<{ correlation?: CorrelationResult; problemId?: string }>()
const emit = defineEmits<{ (e: 'openProblem', id: string): void }>()
const { t } = useI18n()

const res = (r: ResourceRef) => [r.kind, r.namespace, r.name].filter(Boolean).join('/')
const workload = (w: WorkloadRef) => ({
  label: [w.kind, w.namespace, w.name].filter(Boolean).join('/'),
  detail: [
    w.problemPods ? t('incidentsIntel.correlation.pods', { n: w.problemPods }) : '',
    ...(w.symptoms ?? []).map((s) => codeText('symptoms', s)),
  ].filter(Boolean).join(', '),
})

const candidates = computed(() => {
  const c = props.correlation
  return (c?.candidates ?? []).map((cand) => {
    const kindKey = `incidentsCorrelation.kinds.${cand.kind}`
    const affected = (cand.affected ?? []).map(workload)
    return {
      id: cand.id,
      kind: cand.kind,
      kindText: te(kindKey) ? t(kindKey) : t('incidentsIntel.correlation.kindOther'),
      source: res(cand.source),
      confidence: confidenceLevel(cand.confidence),
      since: cand.since ? when(cand.since) : '',
      total: cand.total,
      affected,
      // Усечение: бэкенд пометил или в списке меньше, чем всего.
      hidden: cand.truncated || affected.length < cand.total ? Math.max(cand.total - affected.length, 1) : 0,
      potential: (cand.potential ?? []).map(workload),
      facts: factsByIds(c?.facts ?? [], cand.supporting).map((f) => ({ id: f.id, text: codeText('facts', f.code, f.params) })),
    }
  })
})

/** Неполные данные: «нет прав» — отдельной фразой, остальное — текстом владельца кода. */
const notes = computed(() => {
  const out: string[] = []
  for (const m of props.correlation?.missing ?? []) {
    if (m.params?.reason === 'forbidden') out.push(t('incidentsIntel.correlation.incompleteForbidden'))
    out.push(codeText('missing', m.code, m.params))
  }
  return [...new Set(out)]
})

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<style scoped>
.corr { display: flex; flex-direction: column; gap: 10px; }
.corr h3 { margin: 0; font-size: 15px; color: #cdd6f4; }
.cand { padding: 10px 12px; border-radius: 8px; border-left: 3px dashed #f9e2af; background: rgba(249, 226, 175, 0.05); font-size: 13px; }
.cand h4 { margin: 0 0 4px; font-size: 14px; color: #cdd6f4; }
.cand h5 { margin: 8px 0 4px; font-size: 12px; color: #a6adc8; }
.meta, .note, .muted { margin: 0 0 4px; color: #a6adc8; font-size: 12px; }
.hint { margin: 0 0 4px; color: #f9e2af; font-size: 12px; }
.warn { margin: 4px 0 0; color: #fab387; font-size: 12px; }
.wl { margin: 0; padding-left: 18px; color: #cdd6f4; }
.notes { margin: 0; padding-left: 18px; }
.conf-low { color: #a6adc8; } .conf-medium { color: #f9e2af; } .conf-high { color: #fab387; }
</style>
