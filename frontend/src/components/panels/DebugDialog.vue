<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="debug-overlay" @click="cancel">
    <div class="debug-dialog" role="dialog" aria-modal="true" @click.stop>
      <div class="dialog-header">
        <h3>{{ $t('panels.debug.title', { pod: `${target.namespace}/${target.pod}` }) }}</h3>
      </div>

      <div v-if="loading" class="dialog-content muted">{{ $t('panels.debug.loading') }}</div>

      <form v-else class="dialog-content" @submit.prevent="submit">
        <label class="field">
          <span class="field-label">{{ $t('panels.debug.image') }}</span>
          <select v-if="options?.strict" v-model="image" class="control" data-test="debug-image">
            <option v-for="img in options.images" :key="img" :value="img">{{ img }}</option>
          </select>
          <template v-else>
            <input
              v-model.trim="image"
              class="control mono"
              list="debug-images"
              :placeholder="$t('panels.debug.imagePlaceholder')"
              spellcheck="false"
              autocomplete="off"
              data-test="debug-image"
            />
            <datalist id="debug-images">
              <option v-for="img in options?.images ?? []" :key="img" :value="img" />
            </datalist>
          </template>
          <span v-if="options?.strict" class="hint">{{ $t('panels.debug.imageStrict') }}</span>
        </label>

        <label class="field">
          <span class="field-label">{{ $t('panels.debug.profile') }}</span>
          <select v-model="profile" class="control" data-test="debug-profile">
            <option v-for="p in options?.profiles ?? []" :key="p" :value="p">{{ profileLabel(p) }}</option>
          </select>
        </label>

        <label class="field">
          <span class="field-label">{{ $t('panels.debug.target') }}</span>
          <select v-model="targetContainer" class="control" data-test="debug-target">
            <option value="">{{ $t('panels.debug.noTarget') }}</option>
            <option v-for="c in target.containers" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>

        <p class="hint note">{{ $t('panels.debug.note') }}</p>
        <p v-if="error" class="error" role="alert" data-test="debug-error">{{ error }}</p>
      </form>

      <div class="dialog-footer">
        <button type="button" class="btn btn-cancel" :disabled="starting" @click="cancel">{{ $t('common.cancel') }}</button>
        <button
          type="button"
          class="btn btn-start"
          :disabled="loading || starting || !image"
          data-test="debug-start"
          @click="submit"
        >
          {{ starting ? $t('panels.debug.starting') : $t('panels.debug.start') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { callApp } from '../../platform/api'
import { t, te } from '../../i18n'
import { errorText } from '../../composables/errorText'

/** Ответ GetDebugOptions (Go: app.DebugOptionsInfo) */
interface DebugOptionsInfo {
  images: string[]
  strict: boolean
  profiles: string[]
  defaultProfile: string
}

const props = defineProps<{
  target: { namespace: string; pod: string; containers: string[]; target?: string }
  /** Запускает контейнер и открывает терминал. Ошибка показывается здесь, диалог остаётся открытым. */
  start: (p: { image: string; profile: string; target: string }) => Promise<void>
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const options = ref<DebugOptionsInfo | null>(null)
const loading = ref(true)
const starting = ref(false)
const error = ref('')
const image = ref('')
const profile = ref('')
const targetContainer = ref(props.target.target ?? '')

function profileLabel(p: string): string {
  const key = `panels.debug.profiles.${p}`
  return te(key) ? t(key) : p
}

onMounted(async () => {
  try {
    const o = (await callApp('GetDebugOptions')) as DebugOptionsInfo
    options.value = o
    image.value = o.images[0] ?? ''
    profile.value = o.defaultProfile || o.profiles[0] || ''
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
})

function cancel() {
  if (starting.value) return // запуск уже идёт: контейнер появится в поде, как бы ни закрыли окно
  emit('close')
}

async function submit() {
  if (loading.value || starting.value || !image.value) return
  error.value = ''
  starting.value = true
  try {
    await props.start({ image: image.value, profile: profile.value, target: targetContainer.value })
    emit('close')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    starting.value = false
  }
}
</script>

<style scoped>
.debug-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2000;
}

.debug-dialog {
  background: var(--panel-raised);
  border: 1px solid var(--line-strong);
  border-radius: 8px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5);
  width: 90%;
  max-width: 540px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.dialog-header {
  padding: 16px;
  border-bottom: 1px solid var(--line-strong);
  background: var(--panel-deep);
}

.dialog-header h3 {
  margin: 0;
  color: white;
  font-size: 1rem;
  font-weight: 600;
}

.dialog-content {
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.muted { color: var(--text-soft); }

.field { display: flex; flex-direction: column; gap: 6px; }
.field-label { color: var(--text-soft); font-size: 0.85rem; font-weight: 500; }

.control {
  background: var(--panel);
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  color: var(--text-code);
  padding: 8px 10px;
  font-size: 0.9rem;
  outline: none;
}
.control:focus { border-color: var(--blue-strong); }
.mono { font-family: 'JetBrains Mono', 'Fira Code', monospace; }

.hint { color: var(--text-soft); font-size: 0.8rem; }
.note { margin: 0; }
.error { margin: 0; color: var(--red-soft); font-size: 0.85rem; white-space: pre-wrap; word-break: break-word; }

.dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-top: 1px solid var(--line-strong);
  background: var(--panel-deep);
}

.btn {
  padding: 8px 16px;
  border-radius: 4px;
  border: none;
  font-weight: 600;
  font-size: 0.9rem;
  cursor: pointer;
  transition: all 0.15s;
  white-space: nowrap;
}

.btn-cancel {
  background: transparent;
  border: 1px solid var(--line-strong);
  color: var(--text-soft);
}
.btn-cancel:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.05);
  border-color: #5a5c7a;
  color: var(--text-code);
}

.btn-start { background: var(--blue-strong); color: white; margin-left: auto; }
.btn-start:hover:not(:disabled) { background: var(--blue-deep); }

.btn:disabled { opacity: 0.6; cursor: not-allowed; }
</style>
