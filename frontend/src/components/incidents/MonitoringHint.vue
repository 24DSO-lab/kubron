<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <aside v-if="hints.length" class="hint" data-testid="monitoring-hint" role="note">
    <h3>{{ $t('incidentsIntel.monitoring.title') }}</h3>
    <p v-for="h in hints" :key="h.reason" class="text" data-testid="monitoring-hint-text" :data-reason="h.reason">{{ h.text }}</p>
    <button v-if="installable" class="btn btn-secondary" type="button" data-testid="monitoring-open-settings" @click="openSettings">
      {{ $t('incidentsIntel.monitoring.open') }}
    </button>
  </aside>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../i18n'
import { useK8s } from '../../composables/useK8s'
import type { MissingContext } from '../../types/incident'

/**
 * Подсказка «с мониторингом диагноз точнее» (ADR-0032 §7). Показывается только при MissingContext `metrics.*`;
 * тексты по причинам — у владельца метрик (`incidentsMetrics.hints.<reason>`), неизвестная причина — нейтральная фраза.
 */
const props = defineProps<{ missing?: MissingContext[] }>()
const { t } = useI18n()
const { switchView } = useK8s()

/** Приоритет причин: сначала то, что пользователь может исправить сам. */
const ORDER = ['not_installed', 'forbidden']
const MAX_HINTS = 3

const hints = computed(() => {
  const reasons = new Set<string>()
  for (const m of props.missing ?? []) {
    if (!m.code.startsWith('metrics.')) continue
    reasons.add(m.params?.reason ?? '')
  }
  const sorted = [...reasons].sort((a, b) => rank(a) - rank(b))
  return sorted.slice(0, MAX_HINTS).map((reason) => {
    const key = `incidentsMetrics.hints.${reason}`
    return { reason: te(key) ? reason : 'other', text: te(key) ? t(key) : t('incidentsIntel.monitoring.generic') }
  })
})

const rank = (r: string) => (ORDER.includes(r) ? ORDER.indexOf(r) : ORDER.length)
/** Ссылка на экран мониторинга — только когда стека нет совсем. */
const installable = computed(() => hints.value.some((h) => h.reason === 'not_installed'))

function openSettings() {
  switchView('settings')
}
</script>

<style scoped>
.hint { padding: 10px 12px; border-radius: 8px; border: 1px solid #45475a; background: rgba(137, 180, 250, 0.06); }
.hint h3 { margin: 0 0 6px; font-size: 14px; color: #cdd6f4; }
.text { margin: 0 0 6px; font-size: 13px; color: #a6adc8; }
</style>
