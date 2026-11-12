<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="cov" data-testid="readiness-coverage" :aria-label="$t('readiness.coverage.title')">
    <h3>{{ $t('readiness.coverage.title') }}</h3>
    <p class="cov-lead">{{ $t('readiness.coverage.lead') }}</p>

    <dl class="cov-meta">
      <div><dt>{{ $t('readiness.coverage.mode') }}</dt><dd>{{ $t(`readiness.coverage.modes.${coverage.mode}`) }}</dd></div>
      <div><dt>{{ $t('readiness.coverage.evaluatedAgainst') }}</dt><dd>{{ coverage.evaluatedAgainst }}</dd></div>
      <div>
        <dt>{{ $t('readiness.coverage.apiMatrix') }}</dt>
        <dd>
          <code>{{ coverage.apiMatrix.version }}</code> · {{ $t('readiness.coverage.generated', { date: coverage.apiMatrix.generatedAt }) }}
          <span class="muted"> · {{ $t('readiness.coverage.window', { window: coverage.apiMatrix.window.join(', ') }) }}</span>
        </dd>
      </div>
      <div>
        <dt>{{ $t('readiness.coverage.catalog') }}</dt>
        <dd>
          <code>{{ coverage.catalog.version }}</code> · {{ $t('readiness.coverage.checked', { date: coverage.catalog.checkedAt }) }}
          <span class="muted"> · {{ $t('readiness.coverage.components', { n: coverage.catalog.entries }) }}</span>
        </dd>
      </div>
    </dl>

    <ul class="cov-sources">
      <li v-for="s in coverage.sources" :key="s.kind" :class="['src', stateOf(s)]" :data-testid="`readiness-source-${s.kind}`">
        <div class="src-head">
          <strong>{{ $t(`readiness.coverage.sources.${s.kind}`) }}</strong>
          <span :class="['badge', 'badge-' + stateOf(s)]">{{ $t(`readiness.coverage.state.${stateOf(s)}`) }}</span>
          <span v-if="s.checked && s.observedAt" class="muted">{{ formatDateTime(s.observedAt) }}</span>
        </div>
        <p v-if="s.reason" class="src-reason">{{ messageText(s.reason) }}</p>
        <p v-if="s.methods?.length" class="muted">{{ $t('readiness.coverage.methods') }}: {{ s.methods.map(methodLabel).join(', ') }}</p>
        <ul v-if="s.limitations?.length" class="lim">
          <li v-for="(l, i) in s.limitations" :key="i">{{ messageText(l) }}</li>
        </ul>
        <details v-if="s.scanned?.length" class="kinds">
          <summary>{{ $t('readiness.coverage.scanned', { n: s.scanned.length }) }}</summary>
          <p class="mono">{{ s.scanned.join(', ') }}</p>
        </details>
        <details v-if="s.notCovered?.length" class="kinds" open>
          <summary>{{ $t('readiness.coverage.notCovered', { n: s.notCovered.length }) }}</summary>
          <p class="mono">{{ s.notCovered.join(', ') }}</p>
        </details>
      </li>
    </ul>

    <h4>{{ $t('readiness.coverage.limitations') }}</h4>
    <ul class="lim">
      <li v-for="(l, i) in coverage.limitations" :key="i">{{ messageText(l) }}</li>
    </ul>
  </section>
</template>

<script lang="ts" setup>
import { te, t } from '../../i18n'
import { formatDateTime } from '../../i18n/format'
import { messageText } from './readinessView'
import type { Coverage, SourceCoverage } from '../../types/readiness'

defineProps<{ coverage: Coverage }>()

/** «Не проверено», «частично» и «проверено» — три разных состояния, а не два */
function stateOf(s: SourceCoverage): 'checked' | 'partial' | 'unchecked' {
  if (!s.checked) return 'unchecked'
  return s.partial ? 'partial' : 'checked'
}

const methodLabel = (m: string) => (te(`readiness.coverage.methodNames.${m}`) ? t(`readiness.coverage.methodNames.${m}`) : m)
</script>

<style scoped>
.cov { border: 1px solid #45475a; border-radius: 10px; padding: 16px; margin: 0 0 20px; background: rgba(137, 180, 250, 0.04); }
.cov h3 { margin: 0 0 4px; font-size: 16px; color: #89b4fa; }
.cov h4 { margin: 16px 0 6px; font-size: 13px; color: #a6adc8; }
.cov-lead { margin: 0 0 12px; font-size: 13px; color: #a6adc8; }
.cov-meta { display: grid; gap: 6px; margin: 0 0 14px; font-size: 13px; }
.cov-meta div { display: grid; grid-template-columns: 170px 1fr; gap: 8px; }
.cov-meta dt { color: #6c7086; }
.cov-meta dd { margin: 0; }
.cov-sources { list-style: none; padding: 0; margin: 0; display: grid; gap: 10px; }
.src { border: 1px solid #313244; border-radius: 8px; padding: 10px 12px; }
.src.unchecked { border-color: rgba(249, 226, 175, 0.5); background: rgba(249, 226, 175, 0.06); }
.src.partial { border-color: rgba(250, 179, 135, 0.5); }
.src-head { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.src-reason { margin: 6px 0 0; font-size: 13px; color: #f9e2af; }
.lim { margin: 6px 0 0; padding-left: 18px; font-size: 12px; color: #a6adc8; line-height: 1.5; }
.kinds { margin-top: 6px; font-size: 12px; }
.kinds summary { cursor: pointer; color: #a6adc8; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; word-break: break-word; margin: 4px 0 0; }
.muted { color: #6c7086; font-size: 12px; }
.badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
.badge-checked { background: rgba(166, 227, 161, 0.15); color: #a6e3a1; }
.badge-partial { background: rgba(250, 179, 135, 0.15); color: #fab387; }
.badge-unchecked { background: rgba(249, 226, 175, 0.15); color: #f9e2af; }
code { font-size: 12px; }
</style>
