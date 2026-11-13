<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="fd-overlay" data-testid="flux-delete-overlay" @click.self="close">
    <div
      class="fd"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="fd-title"
      aria-describedby="fd-risk"
      data-testid="flux-delete-dialog"
      @keydown.esc.stop="close"
    >
      <div class="fd-head">
        <h3 id="fd-title" data-testid="flux-delete-title">
          {{ $t('common.delete') }} <span class="fd-name">{{ object.namespace }}/{{ object.name }}</span>
        </h3>
      </div>
      <div class="fd-body">
        <div id="fd-risk" class="fd-risk danger" data-testid="flux-delete-risk">
          <p v-if="consequence" class="fd-line" :data-consequence="consequenceKind" data-testid="flux-delete-consequence">
            {{ $t(consequence.key, consequence.params) }}
          </p>
          <p class="fd-line" data-testid="flux-delete-git">{{ $t('fluxActions.delete.gitRecreates') }}</p>
        </div>
        <label class="fd-label" for="fd-input">{{ $t('fluxActions.delete.typeName', { name: object.name }) }}</label>
        <input
          id="fd-input"
          ref="inputEl"
          v-model="typed"
          class="fd-input"
          type="text"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :disabled="busy"
          data-testid="flux-delete-input"
          @keydown.enter.prevent="submit"
        />
      </div>
      <div class="fd-foot">
        <button type="button" class="fd-btn" :disabled="busy" data-testid="flux-delete-cancel" @click="close">{{ $t('common.cancel') }}</button>
        <button type="button" class="fd-btn danger" :disabled="!canSubmit" data-testid="flux-delete-confirm" @click="submit">
          {{ busy ? $t('fluxActions.delete.deleting') : $t('common.delete') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import { deleteConfirmed, deleteConsequence, deleteConsequenceText, deleteFluxObject } from '../../resources/fluxActions'
import type { FluxHelmReleaseInfo, FluxKustomizationInfo } from '../../types/flux'

const props = defineProps<{ object: FluxKustomizationInfo | FluxHelmReleaseInfo }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'deleted'): void }>()

const typed = ref('')
const busy = ref(false)
const inputEl = ref<HTMLInputElement | null>(null)

// последствия берутся из текущего состояния объекта (prune, inventory), а не из состояния формы
const consequenceInfo = computed(() => deleteConsequence(props.object))
const consequenceKind = computed(() => consequenceInfo.value.kind)
const consequence = computed(() => deleteConsequenceText(consequenceInfo.value))
const canSubmit = computed(() => !busy.value && deleteConfirmed(typed.value, props.object.name))

// пока идёт запрос, диалог не закрывается: иначе результат удаления остаётся без контекста
const close = () => { if (!busy.value) emit('close') }

async function submit() {
  // Enter и клик проходят одну проверку: без точного совпадения имени запроса нет
  if (!canSubmit.value) return
  busy.value = true
  const ok = await deleteFluxObject(props.object)
  busy.value = false
  if (ok) emit('deleted')
  emit('close')
}

onMounted(() => { void nextTick(() => inputEl.value?.focus()) })
</script>

<style scoped>
.fd-overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 2000; }
.fd { width: 90%; max-width: 520px; background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 8px; box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5); text-align: left; }
.fd-head { padding: 16px; background: var(--panel-deep); border-bottom: 1px solid var(--line-strong); border-radius: 8px 8px 0 0; }
.fd-head h3 { margin: 0; font-size: 1rem; color: #fff; overflow-wrap: anywhere; }
.fd-name { color: var(--blue); font-family: 'JetBrains Mono', 'Fira Code', monospace; }
.fd-body { padding: 16px 20px; display: flex; flex-direction: column; gap: 12px; }
.fd-risk { padding: 10px 12px; border-radius: 4px; font-size: 0.8rem; }
.fd-risk.danger { color: #fca5a5; background: rgba(239, 68, 68, 0.1); border: 1px solid rgba(239, 68, 68, 0.4); }
.fd-line { margin: 0; }
.fd-line + .fd-line { margin-top: 6px; }
.fd-label { font-size: 0.85rem; color: var(--text-code); }
.fd-input { padding: 8px 10px; background: var(--panel-deep); color: var(--text-code); border: 1px solid var(--line-strong); border-radius: 4px; font: inherit; font-family: 'JetBrains Mono', 'Fira Code', monospace; }
.fd-input:focus-visible { outline: 2px solid var(--blue); outline-offset: 1px; }
.fd-foot { display: flex; justify-content: flex-end; gap: 10px; padding: 12px 16px; background: var(--panel-deep); border-top: 1px solid var(--line-strong); border-radius: 0 0 8px 8px; }
.fd-btn { padding: 8px 16px; border-radius: 4px; border: 1px solid var(--line-strong); background: transparent; color: var(--text-code); font-size: 0.9rem; font-weight: 600; cursor: pointer; }
.fd-btn:hover:not(:disabled) { background: rgba(255, 255, 255, 0.06); }
.fd-btn.danger { background: #dc2626; border-color: #dc2626; color: #fff; }
.fd-btn.danger:hover:not(:disabled) { background: #b91c1c; }
.fd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.fd-btn:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
</style>
