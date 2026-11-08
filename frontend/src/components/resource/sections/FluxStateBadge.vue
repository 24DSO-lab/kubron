<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <span
    class="rd-badge flux-state"
    :class="tone"
    :data-state="known"
    :title="label"
    data-testid="flux-state-badge"
  ><span class="flux-state-glyph" aria-hidden="true">{{ glyph }}</span>{{ label }}</span>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FluxState } from '../../../types/flux'

// Состояние приходит из Go готовым (ADR-0023, раздел 4): тут только оформление.
// Смысл не держится на цвете: у каждого состояния есть подпись и свой символ.
const props = defineProps<{ state: FluxState }>()
const { t } = useI18n()

const LOOK: Record<FluxState, { tone: string; glyph: string }> = {
  Ready: { tone: 'rd-ok', glyph: '✓' },
  Reconciling: { tone: 'rd-warn', glyph: '↻' },
  Suspended: { tone: 'rd-warn', glyph: '⏸' },
  Failed: { tone: 'rd-err', glyph: '✕' },
  Stalled: { tone: 'rd-err', glyph: '!' },
  Unknown: { tone: 'rd-unk', glyph: '?' },
}

/** Значение вне контракта (новое состояние в будущей версии бэкенда) показывается как Unknown */
const known = computed<FluxState>(() => (Object.hasOwn(LOOK, props.state) ? props.state : 'Unknown'))
const tone = computed(() => LOOK[known.value].tone)
const glyph = computed(() => LOOK[known.value].glyph)
const label = computed(() => t(`flux.state.${known.value}`))
</script>

<style scoped>
.flux-state-glyph { margin-right: 4px; }
</style>
