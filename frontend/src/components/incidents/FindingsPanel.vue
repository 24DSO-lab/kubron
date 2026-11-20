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

    <ChainView v-if="report.chains?.length" :chains="report.chains" :hypotheses="report.hypotheses" />

    <section class="block">
      <h3>{{ $t('incidents.sections.facts') }}</h3>
      <ul class="items">
        <li v-for="f in report.facts" :key="f.id" class="item item-fact" data-testid="fact" data-kind="fact" :data-code="f.code">
          <span class="tag tag-fact">{{ $t('incidents.kindLabels.fact') }}</span>
          <span class="text">{{ codeText('facts', f.code, f.params) }}</span>
          <MetricFacts v-if="isMetricFact(f)" :facts="[f]" />
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
        <li v-for="{ h, role } in orderedHypotheses" :key="h.id" class="item item-hyp" data-testid="hypothesis" data-kind="hypothesis"
          :data-code="h.code" :data-confidence="confidenceLevel(h.confidence)" :data-role="role"
          :class="{ confirmed: incident.outcome?.confirmedHypothesisId === h.id, explained: role === 'explained', candidate: role === 'candidate' }">
          <span class="tag tag-hyp">{{ $t('incidents.kindLabels.hypothesis') }}</span>
          <span class="text">{{ codeText('hypotheses', h.code, h.params) }}</span>
          <span v-if="role === 'candidate'" class="tag tag-cand" data-testid="hypothesis-candidate">{{ $t('incidentsIntel.chain.candidate') }}</span>
          <span v-else-if="role === 'explained'" class="explained-by" data-testid="hypothesis-explained">{{ $t('incidentsIntel.chain.explainedByCandidate') }}</span>
          <span class="conf" :class="`conf-${confidenceLevel(h.confidence)}`" data-testid="confidence">
            {{ $t('incidents.confidence.label', { level: $t(`incidents.confidence.${confidenceLevel(h.confidence)}`) }) }}
          </span>
          <span v-if="incident.outcome?.confirmedHypothesisId === h.id" class="confirmed-by" data-testid="hypothesis-confirmed">
            {{ $t('incidents.outcome.confirmedBy', { hypothesis: codeText('hypotheses', h.code, h.params) }) }}
          </span>
          <span v-else-if="incident.outcome?.rejectedHypothesisIds?.includes(h.id)" class="rejected-by" data-testid="hypothesis-rejected">
            {{ $t('incidentsIntel.outcome.rejectedBadge') }}
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
        <li v-for="r in report.recommendations" :key="r.id" class="item item-rec" data-testid="recommendation" data-kind="recommendation"
          :class="{ helpful: incident.outcome?.helpfulRecommendationIds?.includes(r.id), useless: incident.outcome?.uselessRecommendationIds?.includes(r.id) }">
          <span class="tag tag-rec">{{ $t('incidents.kindLabels.recommendation') }}</span>
          <span class="text">{{ codeText('recommendations', r.code, r.params) }}</span>
          <span v-if="incident.outcome?.helpfulRecommendationIds?.includes(r.id)" class="rec-mark" data-testid="recommendation-helpful">
            {{ $t('incidentsIntel.outcome.helpfulBadge') }}
          </span>
          <span v-else-if="incident.outcome?.uselessRecommendationIds?.includes(r.id)" class="rec-mark" data-testid="recommendation-useless">
            {{ $t('incidentsIntel.outcome.uselessBadge') }}
          </span>
        </li>
      </ul>
    </section>

    <section v-if="report.steps.length" class="block">
      <h3>{{ $t('incidents.sections.steps') }}</h3>
      <ul class="items">
        <li v-for="s in report.steps" :key="s.id" class="item item-step" data-testid="step" data-kind="step" :data-code="s.code">
          <span class="tag tag-step">{{ $t('incidents.kindLabels.step') }}</span>
          <span class="text">{{ codeText('steps', s.code, s.params) }}</span>
        </li>
      </ul>
    </section>

    <section v-if="report.missing.length" class="block">
      <h3>{{ $t('incidents.sections.missing') }}</h3>
      <ul class="items">
        <li v-for="(m, i) in report.missing" :key="i" class="item item-missing" data-testid="missing">{{ m }}</li>
      </ul>
    </section>
  </div>
</template>
