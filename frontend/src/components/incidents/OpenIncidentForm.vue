<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <form class="open" data-testid="open-form" @submit.prevent="submit">
    <label class="field">
      <span>{{ $t('incidents.open.kind') }}</span>
      <select v-model="kind" class="form-input" data-testid="open-kind">
        <option v-for="k in KINDS" :key="k" :value="k">{{ $t(`incidents.kinds.${k}`) }}</option>
      </select>
    </label>
    <label class="field">
      <span>{{ $t('incidents.open.namespace') }}</span>
      <input v-model.trim="namespace" class="form-input" required data-testid="open-namespace" />
    </label>
    <label class="field grow">
      <span>{{ $t('incidents.open.name') }}</span>
      <input v-model.trim="name" class="form-input" required data-testid="open-name" />
    </label>
    <label class="check">
      <input v-model="disableLogs" type="checkbox" data-testid="open-no-logs" />
      {{ $t('incidents.open.noLogs') }}
    </label>
    <button class="btn" type="submit" :disabled="busy || !namespace || !name" data-testid="open-submit">
      {{ busy ? $t('incidents.open.opening') : $t('incidents.open.submit') }}
    </button>
  </form>
</template>

<script lang="ts" setup>
import { ref } from 'vue'
import type { OpenIncidentRequest, WorkloadKind } from '../../types/incident'

const props = defineProps<{ busy?: boolean; initial?: Partial<OpenIncidentRequest> }>()
const emit = defineEmits<{ (e: 'open', r: OpenIncidentRequest): void }>()

const KINDS: WorkloadKind[] = ['Pod', 'Deployment', 'ReplicaSet']
const kind = ref<WorkloadKind>(props.initial?.kind ?? 'Deployment')
const namespace = ref(props.initial?.namespace ?? '')
const name = ref(props.initial?.name ?? '')
const disableLogs = ref(props.initial?.disableLogs ?? false)

function submit() {
  if (!namespace.value || !name.value) return
  emit('open', { kind: kind.value, namespace: namespace.value, name: name.value, disableLogs: disableLogs.value })
}
</script>

<style scoped>
.open { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-end; }
.field { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: #a6adc8; }
.field.grow { flex: 1; min-width: 160px; }
.check { display: flex; gap: 6px; align-items: center; font-size: 12px; color: #a6adc8; }
.form-input { padding: 8px 10px; }
</style>
