<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section v-if="rows.length" class="chains" data-testid="chain-view" :aria-label="$t('incidentsIntel.chain.aria')">
    <h3>{{ $t('incidentsIntel.chain.title') }}</h3>
    <p class="note" data-testid="chain-note">{{ $t('incidentsIntel.chain.note') }}</p>
    <ol v-for="(row, ci) in rows" :key="ci" class="chain" data-testid="chain" :aria-label="$t('incidentsIntel.chain.pathAria', { n: ci + 1 })">
      <li v-for="(s, si) in row.steps" :key="si" class="step" :class="[`h-${s.health}`, { candidate: s.candidate }]" tabindex="0"
        data-testid="chain-step" :data-health="s.health" :data-candidate="s.candidate ? 'true' : undefined" :aria-label="s.aria">
        <span class="res">{{ s.resource }}</span>
        <span class="state" data-testid="chain-state"><span class="icon" aria-hidden="true">{{ s.icon }}</span>{{ s.label }}</span>
        <span v-if="s.detail" class="detail" data-testid="chain-detail">{{ s.detail }}</span>
        <span v-if="s.candidate" class="cand" data-testid="chain-candidate">{{ $t('incidentsIntel.chain.candidate') }}</span>
      </li>
    </ol>
  </section>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import type { Chain, ChainStep, Hypothesis, ResourceRef } from '../../types/incident'

const props = defineProps<{
  chains?: Chain[]
  hypotheses?: Hypothesis[]
}>()
const { t } = useI18n()

/** Нормализованное состояние вершины: неизвестное значение бэкенда не показывается «сырым». */
const KNOWN = ['Healthy', 'Degraded', 'Progressing', 'Suspended', 'Missing', 'Unknown'] as const
type Known = (typeof KNOWN)[number] | 'other'
const ICONS: Record<Known, string> = { Healthy: '✓', Degraded: '!', Progressing: '…', Suspended: '⏸', Missing: '✗', Unknown: '?', other: '·' }

const sameRef = (a: ResourceRef, b: ResourceRef) =>
  (a.group ?? '') === (b.group ?? '') && a.kind === b.kind && (a.namespace ?? '') === (b.namespace ?? '') && a.name === b.name

function describe(step: ChainStep, candidateRef: ResourceRef | undefined) {
  const health: Known = (KNOWN as readonly string[]).includes(step.health) ? (step.health as Known) : 'other'
  const label = t(`incidentsIntel.chain.health.${health}`)
  // «Нет» (Missing) и «не знаем» (Unknown) — разные сообщения; для Unknown обязательна причина.
  let detail = ''
  if (health === 'Missing') detail = t('incidentsIntel.chain.missingHint')
  else if (health === 'Unknown') {
    const key = `incidentsIntel.chain.unknownReasons.${step.reason ?? ''}`
    detail = t('incidentsIntel.chain.unknownHint', { reason: step.reason && te(key) ? t(key) : t('incidentsIntel.chain.unknownReasons.other') })
  } else if (step.reason) {
    const key = `incidentsIntel.chain.reasons.${step.reason}`
    if (te(key)) detail = t(key)
  }
  const resource = `${step.ref.kind}/${step.ref.name}`
  const candidate = !!candidateRef && sameRef(step.ref, candidateRef)
  const aria = [resource, label, detail, candidate ? t('incidentsIntel.chain.candidate') : ''].filter(Boolean).join(', ')
  return { resource, health, label, icon: ICONS[health], detail, candidate, aria }
}

const rows = computed(() =>
  (props.chains ?? [])
    .filter((c) => c.path?.length)
    .map((c) => {
      const cand = c.candidate ? props.hypotheses?.find((h) => h.id === c.candidate)?.resource : undefined
      return { steps: c.path.map((s) => describe(s, cand)) }
    }),
)
</script>

<style scoped>
.chains h3 { margin: 0 0 8px; font-size: 15px; color: #cdd6f4; }
.note { margin: 0 0 8px; color: #a6adc8; font-size: 12px; }
.chain { list-style: none; margin: 0 0 10px; padding: 0; display: flex; flex-wrap: wrap; align-items: stretch; gap: 6px; }
.step { position: relative; display: flex; flex-direction: column; gap: 2px; padding: 6px 10px; border-radius: 8px; border: 1px solid #45475a; background: #1e1e2e; font-size: 12px; color: #cdd6f4; }
.step:not(:last-child)::after { content: '→'; position: absolute; right: -14px; top: 50%; transform: translateY(-50%); color: #6c7086; }
.step:not(:last-child) { margin-right: 10px; }
.step:focus-visible { outline: 2px solid #89b4fa; outline-offset: 2px; }
.res { font-weight: 600; }
.state .icon { display: inline-block; width: 1.2em; font-weight: 700; }
.detail { color: #a6adc8; }
.h-Healthy { border-color: #a6e3a1; }
.h-Degraded { border-color: #fab387; }
.h-Progressing, .h-Suspended { border-color: #89b4fa; }
.h-Missing { border-color: #f38ba8; border-style: solid; background: rgba(243, 139, 168, 0.08); }
.h-Unknown { border-color: #9399b2; border-style: dashed; }
.cand { align-self: flex-start; padding: 0 6px; border-radius: 4px; background: rgba(249, 226, 175, 0.18); color: #f9e2af; font-weight: 600; }
.step.candidate { box-shadow: 0 0 0 2px rgba(249, 226, 175, 0.5); }
</style>
