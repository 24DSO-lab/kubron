<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hi-overlay" @mousedown.self="pressedOverlay = true" @click.self="onOverlayClick">
    <div class="hi-dialog" role="dialog" aria-modal="true" aria-labelledby="hi-title">
      <header class="hi-head">
        <div>
          <div id="hi-title" class="hi-title">{{ $t('helm.install.title', { repo, chart }) }}</div>
          <div class="hi-sub">{{ $t('helm.install.version') }} {{ version }}</div>
        </div>
        <button type="button" class="hi-close" :aria-label="$t('common.close')" :disabled="busy" @click="close">✕</button>
      </header>

      <div v-if="step === 'form'" class="hi-body">
        <div class="hi-fields">
          <label class="hi-field">{{ $t('helm.install.releaseName') }}
            <input v-model.trim="releaseName" class="form-input" :class="{ bad: nameBad }" maxlength="53" autocomplete="off"
              spellcheck="false" />
            <span v-if="nameBad" class="hi-hint bad">{{ $t('helm.install.nsBad') }}</span>
          </label>
          <label class="hi-field">{{ $t('common.namespace') }}
            <input v-model.trim="namespace" class="form-input" :class="{ bad: nsBad }" list="hi-ns" autocomplete="off"
              spellcheck="false" />
            <datalist id="hi-ns"><option v-for="n in namespaceNames" :key="n" :value="n" /></datalist>
            <span v-if="nsBad" class="hi-hint bad">{{ $t('helm.install.nsBad2') }}</span>
          </label>
          <label class="hi-check">
            <input v-model="createNamespace" type="checkbox" /> {{ $t('helm.install.createNs') }}
            <span v-if="nsIsNew" class="hi-hint warn">{{ $t('helm.install.isNew') }}</span>
          </label>
          <label class="hi-check">
            <input v-model="wait" type="checkbox" /> {{ $t('helm.install.waitReady') }}
          </label>
          <label class="hi-field">{{ $t('helm.install.storage') }}
            <select v-model="storage" class="form-input" :aria-label="$t('helm.install.storage')">
              <option value="secret">{{ $t('helm.install.storageSecret') }}</option>
              <option value="configmap">{{ $t('helm.install.storageConfigmap') }}</option>
            </select>
          </label>
        </div>

        <div class="hi-editors">
          <section>
            <h4>{{ $t('helm.install.yourValues') }}</h4>
            <p class="hi-hint">{{ $t('helm.install.yourValuesHint') }}</p>
            <div class="hi-ed"><MonacoEditor v-model="overrides" language="yaml" /></div>
          </section>
          <section>
            <h4>{{ $t('helm.install.chartDefaults') }}</h4>
            <p class="hi-hint">{{ $t('helm.install.chartDefaultsHint') }}</p>
            <div class="hi-ed"><MonacoReadonly :value="defaults || $t('helm.install.valuesUnavailable')" language="yaml" /></div>
          </section>
        </div>
      </div>

      <div v-else class="hi-body">
        <div class="hi-seg" role="group" :aria-label="$t('common.actions')">
          <button type="button" :class="{ on: previewTab === 'manifest' }" @click="previewTab = 'manifest'">{{ $t('helm.install.manifest') }}</button>
          <button type="button" :class="{ on: previewTab === 'notes' }" @click="previewTab = 'notes'">{{ $t('helm.install.notes') }}</button>
        </div>
        <p class="hi-hint">{{ $t('helm.install.previewHint') }}</p>
        <div class="hi-ed hi-ed-tall">
          <MonacoReadonly v-if="previewTab === 'manifest'" :value="preview?.manifest || $t('helm.install.manifestEmpty')" language="yaml" />
          <MonacoReadonly v-else :value="preview?.notes || $t('helm.install.noNotes')" language="plaintext" />
        </div>
      </div>

      <div v-if="busy && liveOp && step === 'preview'" class="hi-live">
        <div class="hi-live-head">
          <span>{{ liveOp.phase }}</span>
          <button v-if="liveOp.cancelable && !liveOp.done" type="button" class="btn" @click="cancelHelmOp(liveOp.opId)">{{ $t('helm.install.cancelInstall') }}</button>
        </div>
        <pre v-if="liveOp.lines.length" class="hi-live-log">{{ liveOp.lines.slice(-6).join('\n') }}</pre>
      </div>

      <p v-if="error" class="form-error hi-error">{{ error }}</p>

      <footer class="hi-foot">
        <template v-if="step === 'form'">
          <button type="button" class="btn" :disabled="busy" @click="close">{{ $t('helm.install.cancel') }}</button>
          <button type="button" class="btn" :disabled="!valid || busy" @click="doPreview">{{ busy ? $t('helm.install.calculating') : $t('helm.install.previewBtn') }}</button>
        </template>
        <template v-else>
          <button type="button" class="btn" :disabled="busy" @click="step = 'form'">{{ $t('helm.install.back') }}</button>
          <button type="button" class="btn hi-primary" :disabled="busy" @click="doInstall">
            {{ busy ? (wait ? $t('helm.install.installingWait') : $t('helm.install.installing')) : $t('helm.install.installBtn') }}
          </button>
        </template>
      </footer>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import MonacoEditor from '../resource/MonacoEditor.vue'
import MonacoReadonly from '../resource/MonacoReadonly.vue'
import { callApp } from '../../resources/wails'
import { cancelHelmOp, helmOps, runHelmOp } from '../../composables/useHelmOps'
import { useK8s } from '../../composables/useK8s'
import { useNotifications } from '../../composables/useNotifications'
import { errorText } from '../../composables/errorText'

interface InstallResult { releaseName: string; namespace: string; revision: number; status: string; manifest: string; notes: string }

const props = defineProps<{ repo: string; chart: string; version: string; defaults: string }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'installed', r: InstallResult): void }>()

const notifications = useNotifications()
const { t } = useI18n()
const { namespaces, selectedNamespaces } = useK8s()

const DNS_LABEL = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/
const RELEASE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/

const releaseName = ref(props.chart.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/^-+|-+$/g, '').slice(0, 53))
const namespace = ref(selectedNamespaces.value[0] || 'default')
const createNamespace = ref(false)
const wait = ref(true)
const storage = ref<'secret' | 'configmap'>('secret')
const overrides = ref('')

const step = ref<'form' | 'preview'>('form')
const previewTab = ref<'manifest' | 'notes'>('manifest')
const preview = ref<InstallResult | null>(null)
const busy = ref(false)
const error = ref('')
const pressedOverlay = ref(false)
const activeOpId = ref('')
const liveOp = computed(() => helmOps.value.find(o => o.opId === activeOpId.value))

const namespaceNames = computed(() => (namespaces.value ?? []).map(n => n.name))
const nameBad = computed(() => releaseName.value !== '' && (releaseName.value.length > 53 || !RELEASE.test(releaseName.value)))
const nsBad = computed(() => namespace.value !== '' && !DNS_LABEL.test(namespace.value))
const nsIsNew = computed(() => DNS_LABEL.test(namespace.value) && !!namespaceNames.value.length && !namespaceNames.value.includes(namespace.value))
const valid = computed(() => !!releaseName.value && !!namespace.value && !nameBad.value && !nsBad.value)

function request(opId = '') {
  return {
    repo: props.repo, chart: props.chart, version: props.version,
    releaseName: releaseName.value, namespace: namespace.value, values: overrides.value,
    createNamespace: createNamespace.value, wait: wait.value, storage: storage.value, opId,
  }
}

async function doPreview() {
  busy.value = true
  error.value = ''
  try {
    preview.value = (await callApp('PreviewHelmInstall', request())) as InstallResult
    previewTab.value = 'manifest'
    step.value = 'preview'
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

async function doInstall() {
  busy.value = true
  error.value = ''
  try {
    const r = (await runHelmOp(
      { op: 'install', namespace: namespace.value, release: releaseName.value, cancelable: true },
      id => callApp('InstallHelmChart', request(id)),
      id => { activeOpId.value = id },
    )) as InstallResult
    notifications.notifySuccess(t('helm.catalog.installed', { name: r.releaseName, ns: r.namespace, rev: r.revision }))
    emit('installed', r)
    emit('close')
  } catch (e: any) {
    error.value = errorText(e)
    step.value = 'form' // можно поправить имя или values и повторить
  } finally {
    busy.value = false
  }
}

function close() { if (!busy.value) emit('close') }
function onOverlayClick() {
  // закрываем по клику на фон только если и нажатие началось на фоне (не при выделении текста)
  if (pressedOverlay.value) close()
  pressedOverlay.value = false
}
function onKey(e: KeyboardEvent) { if (e.key === 'Escape') close() }
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<style scoped>
.hi-overlay { position: fixed; inset: 0; z-index: 1000; background: rgba(0, 0, 0, 0.55); display: flex; align-items: center; justify-content: center; padding: 24px; }
.hi-dialog { width: min(1100px, 100%); max-height: 100%; display: flex; flex-direction: column; background: var(--bg); border: 1px solid var(--surface-0); border-radius: 10px; text-align: left; }
.hi-head { display: flex; justify-content: space-between; align-items: flex-start; padding: 16px 20px; border-bottom: 1px solid var(--surface-0); }
.hi-title { font-size: 18px; color: var(--accent); font-weight: 600; }
.hi-sub { font-size: 12px; color: var(--text-muted); margin-top: 2px; }
.hi-close { background: transparent; border: 0; color: var(--text-muted); font-size: 16px; cursor: pointer; }
.hi-body { padding: 16px 20px; overflow: auto; display: flex; flex-direction: column; gap: 14px; min-height: 0; }
.hi-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 16px; }
.hi-field { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: var(--text-muted); }
.hi-field .form-input { padding: 8px 10px; }
.form-input.bad { border-color: var(--danger); }
.hi-check { grid-column: 1 / -1; display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text); }
.hi-hint { font-size: 12px; color: var(--text-subtle); margin: 0; }
.hi-hint.bad { color: var(--danger); }
.hi-hint.warn { color: var(--warn); }
.hi-editors { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.hi-editors h4 { margin: 0 0 2px; font-size: 13px; color: var(--text); }
.hi-ed { height: 36vh; min-height: 220px; margin-top: 6px; }
.hi-ed-tall { height: 50vh; }
.hi-ed :deep(.mr) { height: 100% !important; min-height: 0; }
.hi-seg { display: inline-flex; align-self: flex-start; border: 1px solid var(--line-strong); border-radius: 4px; overflow: hidden; }
.hi-seg button { padding: 4px 14px; background: transparent; color: var(--gray-400); border: 0; font: inherit; cursor: pointer; }
.hi-seg button.on { background: var(--blue-strong); color: #fff; }
.hi-error { margin: 0 20px; white-space: pre-wrap; max-height: 20vh; overflow: auto; }
.hi-live { margin: 0 20px; padding: 8px 10px; border: 1px solid var(--surface-0); border-radius: 6px; font-size: 13px; color: var(--text-muted); }
.hi-live-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.hi-live-log { margin: 6px 0 0; padding: 6px 8px; background: #12141f; border-radius: 4px; font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 11px; line-height: 1.4; color: var(--gray-400); white-space: pre-wrap; max-height: 120px; overflow: auto; }
.hi-foot { display: flex; justify-content: flex-end; gap: 8px; padding: 14px 20px; border-top: 1px solid var(--surface-0); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.hi-primary { background: var(--blue-strong); color: #fff; }
</style>
