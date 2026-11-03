<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="!can(Perm.SecretsRead)" class="sec-empty" data-testid="secret-no-access">{{ $t('sections.secretData.noAccess') }}</div>
  <div v-else-if="keys.length" class="sec-data">
    <div v-for="k in keys" :key="k" class="sec-item">
      <div class="sec-header">
        <span class="sec-key">{{ k }}</span>
        <div class="sec-actions">
          <button
            class="sec-btn"
            :title="revealed[k] ? $t('sections.secretData.hide') : $t('sections.secretData.reveal')"
            @click="toggleReveal(k)"
          >
            <K8sIcon :name="revealed[k] ? 'view-off' : 'view'" size="sm" />
          </button>
          <button class="sec-btn" :title="$t('common.copy')" @click="copy(k)">
            <K8sIcon name="copy" size="sm" />
          </button>
        </div>
      </div>
      <div class="sec-body">
        <textarea
          v-model="displayValues[k]"
          class="sec-editor"
          rows="4"
          @input="dirty[k] = true"
        />
      </div>
      <div class="sec-footer">
        <span class="sec-mode">{{ revealed[k] ? $t('sections.secretData.decoded') : $t('sections.secretData.encoded') }}</span>
        <button
          class="sec-save"
          :disabled="saving[k] || !dirty[k]"
          @click="saveKey(k)"
        >
          {{ saving[k] ? $t('common.saving') : $t('common.save') }}
        </button>
      </div>
    </div>
  </div>
  <div v-else class="sec-empty">{{ $t('common.noData') }}</div>
</template>

<script lang="ts" setup>
import { ref, computed, reactive, onMounted } from 'vue'
import K8sIcon from '../../icons/K8sIcon.vue'
import { callApp } from '../../../resources/wails'
import { useNotifications } from '../../../composables/useNotifications'
import { t } from '../../../i18n'
import { errorText } from '../../../composables/errorText'
import { can } from '../../../composables/useAuth'
import { Perm } from '../../../types/auth'

const props = defineProps<{ resource: any }>()
const { notifySuccess, notifyError } = useNotifications()

// btoa/atob работают только с Latin-1 и ломают UTF-8 (кириллица, эмодзи): кодируем через байты
function utf8ToB64(text: string): string {
  let bin = ''
  new TextEncoder().encode(text).forEach(b => { bin += String.fromCharCode(b) })
  return btoa(bin)
}

function b64ToUtf8(b64: string): string {
  const bin = atob(b64.trim())
  return new TextDecoder().decode(Uint8Array.from(bin, c => c.charCodeAt(0)))
}

const keys = computed(() => props.resource?.keys ?? [])

// base64Values — оригинальные base64-значения из K8s
const base64Values = reactive<Record<string, string>>({})
// displayValues — то, что показывается в textarea (base64 или decoded)
const displayValues = reactive<Record<string, string>>({})
const revealed = reactive<Record<string, boolean>>({})
const dirty = reactive<Record<string, boolean>>({})
const saving = reactive<Record<string, boolean>>({})

// Load base64 values on mount
onMounted(async () => {
  if (!can(Perm.SecretsRead)) return
  for (const k of keys.value) {
    try {
      const v = await callApp('GetSecretBase64Value', props.resource.namespace, props.resource.name, k)
      base64Values[k] = v ?? ''
      displayValues[k] = v ?? ''
    } catch {
      base64Values[k] = ''
      displayValues[k] = ''
    }
  }
})

function toggleReveal(key: string) {
  if (revealed[key]) {
    // Switch from decoded to base64: encode current display value back to base64
    try {
      displayValues[key] = utf8ToB64(displayValues[key])
    } catch {
      // if encoding fails, keep original base64 value
      displayValues[key] = base64Values[key]
    }
    revealed[key] = false
  } else {
    // Switch from base64 to decoded: decode current display value
    try {
      displayValues[key] = b64ToUtf8(displayValues[key])
    } catch {
      // if decoding fails, keep as-is
    }
    revealed[key] = true
  }
}

function copy(key: string) {
  navigator.clipboard.writeText(displayValues[key] ?? '').catch(() => {})
}

async function saveKey(key: string) {
  const namespace = props.resource?.namespace
  const name = props.resource?.name
  if (!namespace || !name) return

  saving[key] = true
  try {
    const updatedData: Record<string, string> = {}

    for (const k of keys.value) {
      if (k === key) {
        // For the key being saved:
        // If currently in revealed (decoded) mode, encode to base64 before sending
        // If in base64 mode, send as-is
        if (revealed[k]) {
          updatedData[k] = utf8ToB64(displayValues[k] ?? '')
        } else {
          updatedData[k] = displayValues[k] ?? ''
        }
      } else {
        // For other keys, send their original base64 values
        updatedData[k] = base64Values[k] ?? ''
      }
    }

    await callApp('UpdateSecret', namespace, name, updatedData)

    // Update stored base64 values after successful save
    for (const k of keys.value) {
      base64Values[k] = updatedData[k]
    }

    // If we were in decoded mode, switch back to base64 display to show the new encoded value
    if (revealed[key]) {
      displayValues[key] = base64Values[key]
      revealed[key] = false
    }

    dirty[key] = false
    notifySuccess(t('sections.shared.keySaved', { key }))
  } catch (e: any) {
    notifyError(errorText(e) || t('sections.shared.saveFailed'))
  } finally {
    saving[key] = false
  }
}
</script>

<style scoped>
.sec-data {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.sec-item {
  border: 1px solid #2a2b40;
  border-radius: 4px;
  overflow: hidden;
  background: rgba(255, 255, 255, 0.02);
}
.sec-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: #1a1b2e;
  border-bottom: 1px solid #2a2b40;
}
.sec-key {
  font-weight: 600;
  color: #60a5fa;
  font-size: 0.85rem;
}
.sec-actions {
  display: flex;
  gap: 6px;
}
.sec-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 1px solid #3b3d5c;
  border-radius: 4px;
  color: #9ca0b8;
  cursor: pointer;
  transition: all 0.15s;
}
.sec-btn:hover {
  background: rgba(255, 255, 255, 0.1);
  color: white;
}
.sec-body {
  padding: 12px;
}
.sec-editor {
  width: 100%;
  min-height: 80px;
  padding: 8px;
  background: #1a1b26;
  border: 1px solid #2a2b40;
  border-radius: 4px;
  color: #d4d4d4;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', monospace;
  font-size: 0.8rem;
  resize: vertical;
  outline: none;
}
.sec-editor:focus {
  border-color: #3b82f6;
}
.sec-mask {
  color: #6b6d8a;
  font-size: 0.8rem;
}
.sec-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: #1a1b2e;
  border-top: 1px solid #2a2b40;
}
.sec-mode {
  color: #9ca0b8;
  font-size: 0.75rem;
  font-style: italic;
}
.sec-save {
  padding: 4px 12px;
  font-size: 0.8rem;
  font-weight: 500;
  color: #fff;
  background: #3b82f6;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.15s;
}
.sec-save:hover:not(:disabled) {
  background: #2563eb;
}
.sec-save:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.sec-empty {
  padding: 20px;
  text-align: center;
  color: #6b6d8a;
}
</style>
