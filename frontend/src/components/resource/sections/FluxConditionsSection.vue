<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="conditions.length" class="rd-table-wrap" data-testid="flux-conditions">
    <table class="rd-table">
      <thead>
        <tr>
          <th>{{ $t('flux.conditions.type') }}</th>
          <th>{{ $t('flux.conditions.status') }}</th>
          <th>{{ $t('flux.conditions.reason') }}</th>
          <th>{{ $t('flux.conditions.message') }}</th>
          <th>{{ $t('flux.conditions.time') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(c, i) in conditions" :key="`${c.type}-${i}`" data-testid="flux-condition-row">
          <td class="rd-mono">{{ c.type }}</td>
          <td><span class="rd-badge" :class="toneOf(c)">{{ c.status || $t('common.unknown') }}</span></td>
          <td class="rd-mono">{{ c.reason || $t('common.dash') }}</td>
          <td class="rd-muted flux-cond-msg">{{ c.message || $t('common.dash') }}</td>
          <td class="rd-muted">{{ timeOf(c.lastTransitionTime) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
  <div v-else class="rd-empty" data-testid="flux-conditions-empty">{{ $t('flux.conditions.empty') }}</div>
</template>

<script lang="ts">
import type { FluxCondition } from '../../../types/flux'

/**
 * Цвет статуса условия. Ready: True — норма, False — ошибка. Stalled: True — ошибка, Reconciling: True — идёт работа;
 * их False — штатное «нет», поэтому не красится. Остальные типы: True — норма, False — ошибка.
 * Тексты статусов (True/False/Unknown) — значения API, не переводятся.
 */
export function conditionTone(c: Pick<FluxCondition, 'type' | 'status'>): string {
  const on = c.status === 'True'
  const off = c.status === 'False'
  if (c.type === 'Stalled') return on ? 'rd-err' : 'rd-unk'
  if (c.type === 'Reconciling') return on ? 'rd-warn' : 'rd-unk'
  if (on) return 'rd-ok'
  if (off) return 'rd-err'
  return 'rd-unk'
}
</script>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '../../../i18n/format'

import type { FluxObjectInfo } from '../../../types/flux'

// Условия (status.conditions) любого объекта Flux; контроллеры сами отдают их в нужном порядке
const props = defineProps<{ resource: Partial<FluxObjectInfo> }>()
const { t } = useI18n()
const conditions = computed(() => props.resource?.conditions ?? [])
const toneOf = conditionTone

/** Время перехода; пустое или некорректное значение — прочерк, а не «Invalid Date» */
function timeOf(value?: string): string {
  if (!value || Number.isNaN(Date.parse(value))) return t('common.dash')
  return formatDateTime(value)
}
</script>

<style scoped>
.flux-cond-msg { max-width: 480px; overflow-wrap: anywhere; }
</style>
