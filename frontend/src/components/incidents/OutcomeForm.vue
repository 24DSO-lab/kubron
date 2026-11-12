<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="outcome" data-testid="outcome">
    <div v-if="incident.outcome" class="done" data-testid="outcome-done">
      <p class="result">{{ $t('incidents.outcome.done', { result: $t(`incidents.outcome.results.${incident.outcome.result}`) }) }}</p>
      <p class="by">{{ $t('incidents.outcome.by', { user: incident.outcome.markedBy, time: when(incident.outcome.markedAt) }) }}</p>
      <p v-if="incident.outcome.note" class="note" data-testid="outcome-note">{{ incident.outcome.note }}</p>
    </div>
    <form v-else class="form" @submit.prevent="submit">
      <label class="field">
        <span>{{ $t('incidents.outcome.result') }}</span>
        <select v-model="result" class="form-input" data-testid="outcome-result">
          <option v-for="r in RESULTS" :key="r" :value="r">{{ $t(`incidents.outcome.results.${r}`) }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ $t('incidents.outcome.confirmed') }}</span>
        <select v-model="confirmed" class="form-input" data-testid="outcome-hypothesis">
          <option value="">{{ $t('incidents.outcome.noneConfirmed') }}</option>
          <option v-for="h in incident.report.hypotheses" :key="h.id" :value="h.id">{{ codeText('hypotheses', h.code, h.params) }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ $t('incidents.outcome.note') }}</span>
        <textarea v-model="note" class="form-input" rows="3" maxlength="2000" data-testid="outcome-note-input"></textarea>
      </label>
      <button class="btn" type="submit" :disabled="busy" data-testid="outcome-submit">{{ $t('incidents.outcome.submit') }}</button>
    </form>
  </div>
</template>

<script lang="ts" setup>
import { ref } from 'vue'
import { codeText } from '../../composables/incidentModel'
import type { Incident, OutcomeResult } from '../../types/incident'

defineProps<{ incident: Incident; busy?: boolean }>()
const emit = defineEmits<{ (e: 'submit', v: { result: OutcomeResult; confirmed: string; note: string }): void }>()

const RESULTS: OutcomeResult[] = ['resolved', 'unresolved', 'inconclusive']
const result = ref<OutcomeResult>('resolved')
const confirmed = ref('')
const note = ref('')

function submit() {
  emit('submit', { result: result.value, confirmed: confirmed.value, note: note.value })
}

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<style scoped>
.form { display: flex; flex-direction: column; gap: 10px; max-width: 520px; }
.field { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: #a6adc8; }
.form-input { padding: 8px 10px; }
.result { margin: 0; font-weight: 600; color: #a6e3a1; }
.by { margin: 2px 0 0; font-size: 12px; color: #a6adc8; }
.note { margin: 8px 0 0; white-space: pre-wrap; color: #cdd6f4; font-size: 13px; }
</style>
