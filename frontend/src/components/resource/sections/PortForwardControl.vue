<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="can(Perm.PortForward)" class="pf-control">
    <div v-if="active" class="pf-active">
      <span class="pf-label">→ {{ active.mode === 'proxy' ? $t('sections.portForward.viaProxy') : `localhost:${active.localPort}` }}</span>
      <button class="pf-btn-stop" @click="stop">{{ $t('defs.menu.stop') }}</button>
    </div>
    <template v-else>
      <button class="pf-btn" @click="openModal">{{ $t('sections.portForward.forward') }}</button>
    </template>

    <div v-if="showModal" class="pf-modal-overlay" @click="showModal = false">
      <div class="pf-modal" @click.stop>
        <div class="pf-modal-head">
          <span>{{ $t('sections.portForward.title', { type: resourceType, name: resourceName }) }}</span>
          <button class="pf-close" @click="showModal = false">✕</button>
        </div>
        <div class="pf-modal-body">
          <div v-if="!proxy" class="pf-row">
            <label class="pf-row-label">{{ $t('sections.portForward.localPort') }}</label>
            <div class="pf-port-input-wrap">
              <input
                ref="portInputRef"
                v-model="localPortInput"
                type="text"
                class="pf-input"
                :placeholder="isRandom ? $t('sections.portForward.random') : ''"
                @focus="onPortFocus"
                @blur="onPortBlur"
                @keyup.enter="start"
              />
            </div>
          </div>
          <label v-if="!proxy" class="pf-check">
            <input v-model="useHttps" type="checkbox" />
            <span>https</span>
          </label>
          <label class="pf-check">
            <input v-model="openInBrowser" type="checkbox" />
            <span>{{ $t('sections.portForward.openInBrowser') }}</span>
          </label>
        </div>
        <div class="pf-modal-foot">
          <button class="pf-btn-secondary" @click="showModal = false">{{ $t('common.cancel') }}</button>
          <button class="pf-btn-primary" @click="start" :disabled="loading">{{ loading ? '...' : $t('sections.portForward.start') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed } from 'vue'
import { useK8s } from '../../../composables/useK8s'
import { useNotifications } from '../../../composables/useNotifications'
import { can } from '../../../composables/useAuth'
import { Perm } from '../../../types/auth'
import { callApp } from '../../../resources/wails'
import { openUrl } from '../../../platform/files'
import { t } from '../../../i18n'
import { errorText } from '../../../composables/errorText'
import { getPortForwardUrl, isProxyForward, stopPortForward } from '../../../composables/portForwardUrl'

const props = defineProps<{
  namespace: string
  resourceName: string
  port: number
  resourceType: 'pod' | 'service'
}>()

const k8s = useK8s()
const notifications = useNotifications()
const showModal = ref(false)
const localPortInput = ref('')
const isRandom = ref(true)
const useHttps = ref(false)
const openInBrowser = ref(true)
const loading = ref(false)
const proxy = computed(() => isProxyForward())

const active = computed(() => {
  return k8s.portForwards.value.find((s: any) => {
    if (s.namespace !== props.namespace) return false
    if (props.resourceType === 'service') {
      return s.service === props.resourceName && s.servicePort === props.port
    }
    return !s.service && s.pod === props.resourceName && s.podPort === props.port
  })
})

function openModal() {
  showModal.value = true
  isRandom.value = true
  localPortInput.value = ''
  useHttps.value = false
  openInBrowser.value = true
}

function onPortFocus() {
  if (isRandom.value) {
    localPortInput.value = ''
    isRandom.value = false
  }
}

function onPortBlur() {
  const val = parseInt(localPortInput.value.trim(), 10)
  if (!val || isNaN(val)) {
    isRandom.value = true
    localPortInput.value = ''
  }
}

async function start() {
  if (loading.value) return
  loading.value = true
  try {
    // В режиме proxy порт на хосте не открывается: локальный порт не нужен и сервером игнорируется
    let localPort = 0
    const inputVal = parseInt(localPortInput.value.trim(), 10)
    if (!proxy.value) {
      localPort = inputVal && !isNaN(inputVal) ? inputVal : await callApp('GetFreePort')
    }

    const session = props.resourceType === 'pod'
      ? await callApp('StartPortForward', props.namespace, props.resourceName, localPort, props.port)
      : await callApp('StartServicePortForward', props.namespace, props.resourceName, localPort, props.port)
    await k8s.refreshPortForwards()
    showModal.value = false
    const target = `${props.resourceName}:${props.port}`
    notifications.notifySuccess(proxy.value
      ? t('sections.portForward.startedProxy', { target })
      : t('sections.portForward.started', { port: session?.localPort ?? localPort, target }))

    if (openInBrowser.value) openUrl(await getPortForwardUrl(session.id, useHttps.value))
  } catch (e: any) {
    // Окно остаётся открытым: порт можно поправить и нажать Start ещё раз
    notifications.notifyError(t('sections.portForward.startError', { error: errorText(e) }))
  } finally {
    loading.value = false
  }
}

async function stop() {
  if (!active.value) return
  try {
    await stopPortForward(active.value.id)
    await k8s.refreshPortForwards()
    notifications.notifySuccess(t('sections.portForward.stopped'))
  } catch (e: any) {
    notifications.notifyError(t('sections.portForward.stopError', { error: errorText(e) }))
  }
}
</script>

<style scoped>
.pf-control { display: inline-flex; align-items: center; }
.pf-btn { padding: 2px 8px; background: transparent; border: 1px solid var(--line-strong); border-radius: 4px; color: var(--text-code); cursor: pointer; font-size: 0.75rem; }
.pf-btn:hover { background: rgba(59,130,246,0.2); border-color: var(--blue); color: var(--blue); }
.pf-active { display: flex; align-items: center; gap: 6px; }
.pf-label { color: #34d399; font-size: 0.75rem; font-weight: 500; }
.pf-btn-stop { padding: 2px 8px; background: transparent; border: 1px solid var(--red); border-radius: 4px; color: var(--red); cursor: pointer; font-size: 0.75rem; }
.pf-btn-stop:hover { background: rgba(239,68,68,0.2); }

.pf-modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.pf-modal { background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 8px; width: 90%; max-width: 420px; overflow: hidden; }
.pf-modal-head { display: flex; align-items: center; justify-content: space-between; padding: 14px 18px; border-bottom: 1px solid var(--line); font-weight: 600; font-size: 0.95rem; }
.pf-close { background: transparent; border: none; color: var(--text-soft); cursor: pointer; font-size: 1rem; }
.pf-close:hover { color: var(--text-code); }
.pf-modal-body { padding: 18px; display: flex; flex-direction: column; gap: 14px; }
.pf-row { display: flex; align-items: center; gap: 12px; }
.pf-row-label { color: var(--text-soft); font-size: 0.85rem; font-weight: 500; white-space: nowrap; }
.pf-port-input-wrap { flex: 1; }
.pf-input { padding: 6px 10px; background: var(--panel); border: 1px solid var(--line-strong); border-radius: 4px; color: white; font-size: 0.9rem; width: 100%; }
.pf-input:focus { outline: none; border-color: var(--blue-strong); }
.pf-input::placeholder { color: var(--gray-500); font-style: italic; }
.pf-check { display: flex; align-items: center; gap: 8px; cursor: pointer; font-size: 0.85rem; color: var(--text-code); }
.pf-check input[type="checkbox"] { width: 16px; height: 16px; accent-color: var(--blue-strong); cursor: pointer; }
.pf-modal-foot { display: flex; justify-content: flex-end; gap: 10px; padding: 14px 18px; border-top: 1px solid var(--line); }
.pf-btn-secondary { padding: 7px 16px; background: var(--surface-1); border: none; border-radius: 6px; color: var(--text); cursor: pointer; font-size: 0.85rem; }
.pf-btn-secondary:hover { background: var(--surface-2); }
.pf-btn-primary { padding: 7px 16px; background: var(--blue-strong); border: none; border-radius: 6px; color: white; cursor: pointer; font-size: 0.85rem; }
.pf-btn-primary:hover:not(:disabled) { background: var(--blue-deep); }
.pf-btn-primary:disabled { opacity: 0.6; cursor: not-allowed; }
</style>