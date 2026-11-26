<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hc">
    <div class="hc-bar">
      <label v-if="revisions.length > 1" class="hc-field">
        {{ $t('defs.columns.revision') }}
        <select v-model.number="revision" class="hc-select" :aria-label="$t('defs.columns.revision')">
          <option v-for="r in revisions" :key="r.revision" :value="r.revision">#{{ r.revision }} · {{ r.status }}</option>
        </select>
      </label>
      <div v-if="kind === 'values'" class="hc-seg" role="group" :aria-label="$t('helm.content.modeAria')">
        <button :class="{ on: mode === 'user' }" @click="mode = 'user'">{{ $t('helm.content.userSupplied') }}</button>
        <button :class="{ on: mode === 'all' }" @click="mode = 'all'">{{ $t('helm.content.allValues') }}</button>
      </div>
      <button class="rd-btn hc-copy" :disabled="!text" @click="copy">{{ copied ? $t('helm.content.copied') : $t('common.copy') }}</button>
    </div>

    <div v-if="error" class="rd-empty">{{ error }}</div>
    <div v-else-if="loading && !content" class="rd-empty">{{ $t('common.loading') }}</div>
    <div v-else-if="kind === 'notes' && !text" class="rd-empty">{{ $t('helm.content.noNotes') }}</div>
    <MonacoReadonly v-else :value="text" :language="language" />
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import MonacoReadonly from '../MonacoReadonly.vue'
import { loadHelmContent } from '../../../resources/helmContent'
import { errorText } from '../../../composables/errorText'
import { itemName, itemNs } from '../../../resources/utils'
import type { k8s } from '../../../../wailsjs/go/models'

const props = defineProps<{ resource: any; kind: 'values' | 'notes' | 'manifest' }>()
const { t } = useI18n()

const ns = computed(() => itemNs(props.resource))
const name = computed(() => itemName(props.resource))
const revisions = computed<{ revision: number; status: string }[]>(() => props.resource?.history ?? [])

const revision = ref<number>(Number(props.resource?.revision) || 0)
// Релиз обновился (появилась новая ревизия): показываем её
watch(() => props.resource?.revision, r => { if (r) revision.value = Number(r) })

const mode = ref<'user' | 'all'>('user')
const content = shallowRef<k8s.HelmReleaseContent | null>(null)
const loading = ref(false)
const error = ref('')
const copied = ref(false)

let seq = 0
async function load() {
  const my = ++seq
  loading.value = true
  error.value = ''
  try {
    const c = await loadHelmContent(ns.value, name.value, revision.value)
    if (my !== seq) return
    content.value = c
  } catch (e: any) {
    if (my !== seq) return
    content.value = null
    error.value = errorText(e)
  } finally {
    if (my === seq) loading.value = false
  }
}
watch([ns, name, revision], load, { immediate: true })

const text = computed(() => {
  const c = content.value
  if (!c) return ''
  switch (props.kind) {
    case 'values':
      return mode.value === 'all' ? c.computedValues : c.values || `${t('helm.content.noUserValues')}\n`
    case 'notes':
      return c.notes
    default:
      return c.manifest
  }
})
const language = computed(() => (props.kind === 'notes' ? 'plaintext' : 'yaml'))

async function copy() {
  try {
    await navigator.clipboard.writeText(text.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 1500)
  } catch { /* буфер обмена недоступен */ }
}
</script>

<style scoped>
.hc-bar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.hc-field { display: flex; align-items: center; gap: 6px; color: var(--gray-400); }
.hc-select { padding: 5px 8px; background: var(--panel); color: var(--text-code); border: 1px solid var(--line-strong); border-radius: 4px; font: inherit; }
.hc-seg { display: inline-flex; border: 1px solid var(--line-strong); border-radius: 4px; overflow: hidden; }
.hc-seg button { padding: 4px 10px; background: transparent; color: var(--gray-400); border: 0; font: inherit; cursor: pointer; }
.hc-seg button.on { background: var(--blue-strong); color: #fff; }
.hc-copy { margin-left: auto; }
</style>