<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="dd-overlay" @click.self="close">
    <div class="dd" role="dialog" aria-modal="true">
      <div class="dd-head">
        <h3>{{ $t('common.delete') }} <span class="dd-name">{{ app.namespace }}/{{ app.name }}</span></h3>
      </div>
      <div class="dd-body">
        <label class="dd-check">
          <input v-model="cascade" type="checkbox" />
          {{ $t('argo.delete.cascade') }}
        </label>
        <div class="dd-risk" :class="{ danger: cascade }">
          <template v-if="cascade">
            {{ $t('argo.delete.cascadeRisk', app.resources?.length ?? 0) }}
          </template>
          <template v-else>
            {{ $t('argo.delete.keepRisk') }}
          </template>
        </div>
      </div>
      <div class="dd-foot">
        <button class="dd-btn" :disabled="busy" @click="close">{{ $t('common.cancel') }}</button>
        <button class="dd-btn danger" :disabled="busy" @click="submit">{{ busy ? $t('argo.delete.deleting') : $t('common.delete') }}</button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref } from 'vue'
import { deleteArgoApplication } from '../../resources/argoActions'
import type { ArgoApplicationInfo } from '../../types/argocd'

const props = defineProps<{ app: ArgoApplicationInfo }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'deleted'): void }>()

const cascade = ref(true)
const busy = ref(false)

const close = () => { if (!busy.value) emit('close') }

async function submit() {
  busy.value = true
  const ok = await deleteArgoApplication(props.app, cascade.value)
  busy.value = false
  if (ok) emit('deleted')
  emit('close')
}
</script>

<style scoped>
.dd-overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 2000; }
.dd { width: 90%; max-width: 480px; background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 8px; box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5); text-align: left; }
.dd-head { padding: 16px; background: var(--panel-deep); border-bottom: 1px solid var(--line-strong); border-radius: 8px 8px 0 0; }
.dd-head h3 { margin: 0; font-size: 1rem; color: var(--text-strong); }
.dd-name { color: var(--blue); font-family: 'JetBrains Mono', 'Fira Code', monospace; }
.dd-body { padding: 16px 20px; display: flex; flex-direction: column; gap: 12px; }
.dd-check { display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: var(--text-code); cursor: pointer; }
.dd-risk { padding: 10px 12px; border-radius: 4px; font-size: 0.8rem; color: var(--text-soft); background: rgba(var(--tint-rgb), 0.03); border: 1px solid var(--line); }
.dd-risk.danger { color: #fca5a5; background: rgba(239, 68, 68, 0.1); border-color: rgba(239, 68, 68, 0.4); }
.dd-foot { display: flex; justify-content: flex-end; gap: 10px; padding: 12px 16px; background: var(--panel-deep); border-top: 1px solid var(--line-strong); border-radius: 0 0 8px 8px; }
.dd-btn { padding: 8px 16px; border-radius: 4px; border: 1px solid var(--line-strong); background: transparent; color: var(--text-code); font-size: 0.9rem; font-weight: 600; cursor: pointer; }
.dd-btn:hover:not(:disabled) { background: rgba(var(--tint-rgb), 0.06); }
.dd-btn.danger { background: #dc2626; border-color: #dc2626; color: #fff; }
.dd-btn.danger:hover:not(:disabled) { background: #b91c1c; }
.dd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>