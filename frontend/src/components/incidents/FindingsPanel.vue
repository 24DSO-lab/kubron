<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="findings" data-testid="findings">
    <section v-if="report.symptoms.length" class="block">
      <h3>{{ $t('incidents.sections.symptoms') }}</h3>
      <div class="chips">
        <span v-for="s in report.symptoms" :key="s" class="chip" data-testid="symptom" :data-code="s">{{ codeText('symptoms', s) }}</span>
      </div>
    </section>

    <p v-if="report.partial?.length" class="note warn" data-testid="partial">
      {{ $t('incidents.partial', { sources: report.partial.join(', ') }) }}
    </p>

    <section class="block">
      <h3>{{ $t('incidents.sections.facts') }}</h3>
      <ul class="items">
        <li v-for="f in report.facts" :key="f.id" class="item item-fact" data-testid="fact" data-kind="fact" :data-code="f.code">
          <span class="tag tag-fact">{{ $t('incidents.kindLabels.fact') }}</span>
          <span class="text">{{ codeText('facts', f.code, f.params) }}</span>
          <div class="meta">
            {{ $t('incidents.source.label', { source: sourceText(f) }) }} · {{ $t('incidents.observedAt', { time: when(f.observedAt) }) }}
          </div>
          <div v-if="f.log" class="evidence-state" data-testid="fact-evidence">
            <template v-if="evidenceFor(incident, f)?.status === 'saved'">
              <span class="ev-ok" data-testid="evidence-saved">{{ $t('incidents.evidence.saved') }}</span>
            </template>
            <template v-else>
              <span class="ev-no" data-testid="evidence-not-saved">
                {{ $t('incidents.evidence.notSaved', { reason: reasonText(evidenceFor(incident, f)?.reason ?? 'log_unavailable') }) }}
              </span>
            </template>
          </div>
        </li>
      </ul>
    </section>

    <section v-if="report.hypotheses.length" class="block">
      <h3>{{ $t('incidents.sections.hypotheses') }}</h3>
      <p class="note" data-testid="hypothesis-note">{{ $t('incidents.hypothesisNote') }}</p>
      <ul class="items">
        <li v-for="h in report.hypotheses" :key="h.id" class="item item-hyp" data-testid="hypothesis" data-kind="hypothesis"
          :data-code="h.code" :data-confidence="confidenceLevel(h.confidence)"
          :class="{ confirmed: incident.outcome?.confirmedHypothesisId === h.id }">
          <span class="tag tag-hyp">{{ $t('incidents.kindLabels.hypothesis') }}</span>
          <span class="text">{{ codeText('hypotheses', h.code, h.params) }}</span>
          <span class="conf" :class="`conf-${confidenceLevel(h.confidence)}`" data-testid="confidence">
            {{ $t('incidents.confidence.label', { level: $t(`incidents.confidence.${confidenceLevel(h.confidence)}`) }) }}
          </span>
          <span v-if="incident.outcome?.confirmedHypothesisId === h.id" class="confirmed-by" data-testid="hypothesis-confirmed">
            {{ $t('incidents.outcome.confirmedBy', { hypothesis: codeText('hypotheses', h.code, h.params) }) }}
          </span>
          <div class="refs" data-testid="supporting">
            <strong>{{ $t('incidents.supporting') }}:</strong>
            <template v-if="support(h).length">
              <span v-for="f in support(h)" :key="f.id" class="ref">{{ codeText('facts', f.code, f.params) }}</span>
            </template>
            <span v-else class="muted">{{ $t('incidents.none') }}</span>
          </div>
          <div class="refs" data-testid="contradicting">
            <strong>{{ $t('incidents.contradicting') }}:</strong>
            <template v-if="contra(h).length">
              <span v-for="f in contra(h)" :key="f.id" class="ref">{{ codeText('facts', f.code, f.params) }}</span>
            </template>
            <span v-else class="muted">{{ $t('incidents.none') }}</span>
          </div>
        </li>
      </ul>
    </section>

    <section v-if="report.recommendations.length" class="block">
      <h3>{{ $t('incidents.sections.recommendations') }}</h3>
      <ul class="items">
        <li v-for="r in report.recommendations" :key="r.id" class="item item-rec" data-testid="recommendation" data-kind="recommendation">
          <span class="tag tag-rec">{{ $t('incidents.kindLabels.recommendation') }}</span>
          <span class="text">{{ codeText('recommendations', r.code, r.params) }}</span>
        </li>
      </ul>
    </section>

    <section v-if="report.steps.length" class="block">
      <h3>{{ $t('incidents.sections.steps') }}</h3>
      <ul class="items">
        <li v-for="s in report.steps" :key="s.id" class="item item-step" data-testid="step" data-kind="step">
          <span class="tag tag-step">{{ $t('incidents.kindLabels.step') }}</span>
          <span class="text">{{ codeText('steps', s.code, s.params) }}</span>
        </li>
      </ul>
    </section>

    <section v-if="report.missing.length" class="block" data-testid="missing-context">
      <h3>{{ $t('incidents.sections.missing') }}</h3>
      <ul class="items">
        <li v-for="(m, i) in report.missing" :key="i" class="item item-missing" data-testid="missing" :data-code="m.code">
          {{ codeText('missing', m.code, m.params) }}
        </li>
      </ul>
    </section>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { codeText, confidenceLevel, evidenceFor, factsByIds, reasonText } from '../../composables/incidentModel'
import type { Fact, Hypothesis, Incident } from '../../types/incident'

const props = defineProps<{ incident: Incident }>()
const { t } = useI18n()
const report = computed(() => props.incident.report)

const support = (h: Hypothesis) => factsByIds(report.value.facts, h.supporting)
const contra = (h: Hypothesis) => factsByIds(report.value.facts, h.contradicting)

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function sourceText(f: Fact): string {
  const kind = t(`incidents.source.kinds.${f.source.kind}`)
  const where = f.source.field ? `${f.source.resource.kind}/${f.source.resource.name} ${f.source.field}` : `${f.source.resource.kind}/${f.source.resource.name}`
  return `${kind}: ${where}`
}
</script>

<style scoped>
.findings { display: flex; flex-direction: column; gap: 18px; }
.block h3 { margin: 0 0 8px; font-size: 15px; color: #cdd6f4; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip { padding: 3px 10px; border-radius: 999px; background: #313244; color: #cdd6f4; font-size: 12px; }
.items { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.item { padding: 10px 12px; border-radius: 8px; border-left: 3px solid #45475a; background: #1e1e2e; font-size: 13px; }
.item-fact { border-left-color: #89b4fa; }
.item-hyp { border-left-style: dashed; border-left-color: #f9e2af; background: rgba(249, 226, 175, 0.05); }
.item-hyp.confirmed { border-left-style: solid; border-left-color: #a6e3a1; }
.item-rec { border-left-color: #a6e3a1; }
.item-step { border-left-color: #94e2d5; }
.item-missing { border-left-color: #fab387; color: #fab387; }
.tag { display: inline-block; margin-right: 8px; padding: 1px 7px; border-radius: 4px; font-size: 11px; font-weight: 600; }
.tag-fact { background: rgba(137, 180, 250, 0.18); color: #89b4fa; }
.tag-hyp { background: rgba(249, 226, 175, 0.18); color: #f9e2af; }
.tag-rec { background: rgba(166, 227, 161, 0.18); color: #a6e3a1; }
.tag-step { background: rgba(148, 226, 213, 0.18); color: #94e2d5; }
.text { color: #cdd6f4; }
.meta, .refs { margin-top: 4px; color: #a6adc8; font-size: 12px; }
.refs .ref { display: inline-block; margin: 0 6px 2px 0; padding: 0 6px; border-radius: 4px; background: #313244; }
.conf { margin-left: 8px; font-size: 12px; }
.conf-low { color: #a6adc8; } .conf-medium { color: #f9e2af; } .conf-high { color: #fab387; }
.confirmed-by { display: block; margin-top: 4px; color: #a6e3a1; font-size: 12px; }
.note { margin: 0 0 8px; color: #a6adc8; font-size: 12px; }
.note.warn { color: #f9e2af; }
.muted { color: #6c7086; }
.evidence-state { margin-top: 4px; font-size: 12px; }
.ev-ok { color: #a6e3a1; } .ev-no { color: #fab387; }
</style>
