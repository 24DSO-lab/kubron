<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="incidents-view" data-testid="incidents-view">
    <header class="head">
      <div>
        <h2>{{ $t('incidents.title') }}</h2>
        <p class="sub">{{ $t('incidents.subtitle') }}</p>
      </div>
      <div class="actions">
        <button class="btn btn-secondary" type="button" :disabled="loading" data-testid="refresh-list" @click="reload">{{ $t('incidents.refresh') }}</button>
        <button class="btn btn-secondary" type="button" data-testid="toggle-settings" @click="toggleSettings">{{ $t('incidents.settings.open') }}</button>
      </div>
    </header>

    <p v-if="loadError" class="form-error" role="alert" data-testid="incident-error">{{ loadError }}</p>

    <IncidentSettings v-if="settingsOpen && settings" :info="settings" :busy="busy" @save="saveSettings" />

    <template v-if="detail">
      <IncidentDetail :incident="detail.incident" :fragments="detail.fragments" :can-read-logs="canReadLogs" :read="readFragment" :busy="busy"
        @back="close" @refresh="run(refreshEvents)" @export="(w) => run(() => exportIncident(w))" @delete="askDelete"
        @save-fragment="(ev) => run(() => saveFragment(ev.pod, ev.container, !!ev.previous), 'evidence.saveDone')"
        @delete-fragment="askDeleteFragment" @delete-all-fragments="askDeleteAll"
        @capture="(on) => run(() => setLogCapture(on, false))"
        @outcome="(v) => run(() => markOutcome(v.result, v.confirmed, v.note))" />
    </template>
    <template v-else>
      <section class="panel">
        <h3>{{ $t('incidents.open.title') }}</h3>
        <OpenIncidentForm :busy="busy" @open="openIncident" />
      </section>
      <section class="panel">
        <h3>{{ $t('incidents.list.title') }}</h3>
        <IncidentList :items="items" :status="status" :loading="loading" :has-more="!!nextCursor" @select="select"
          @filter="setFilter" @more="loadList(true)" />
      </section>
    </template>

    <ConfirmationDialog v-model="confirm.open" resource-type="" resource-name="" :title="confirm.title" @confirm="confirm.action()" />
  </div>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import { useClusters } from '../../composables/useClusters'
import { useIncidents } from '../../composables/useIncidents'
import { useNotifications } from '../../composables/useNotifications'
import ConfirmationDialog from '../utils/ConfirmationDialog.vue'
import IncidentDetail from '../incidents/IncidentDetail.vue'
import IncidentList from '../incidents/IncidentList.vue'
import IncidentSettings from '../incidents/IncidentSettings.vue'
import OpenIncidentForm from '../incidents/OpenIncidentForm.vue'
import type { IncidentSettings as Settings, IncidentStatus, OpenIncidentRequest } from '../../types/incident'

const { t } = useI18n()
const { activeClusterId } = useClusters()
const notifications = useNotifications()
const {
  items, nextCursor, status, loading, detail, settings, canReadLogs,
  loadList, open, select: selectIncident, close, refreshEvents, markOutcome, saveFragment, deleteFragment, setLogCapture,
  readFragment, remove, exportIncident, loadSettings, saveSettings: persistSettings, listen,
} = useIncidents(() => activeClusterId.value)

const busy = ref(false)
const loadError = ref('')
const settingsOpen = ref(false)
const confirm = reactive({ open: false, title: '', action: () => {} })
let unlisten: (() => void) | null = null

/** Выполняет действие с блокировкой кнопок; ошибка бэкенда показывается, а не теряется. */
async function run(action: () => Promise<unknown>, successKey = '') {
  busy.value = true
  loadError.value = ''
  try {
    await action()
    if (successKey) notifications.notifySuccess(t(`incidents.${successKey}`))
  } catch (e) {
    loadError.value = errorText(e)
  } finally {
    busy.value = false
  }
}

async function reload() {
  await run(() => loadList())
}

function setFilter(s: IncidentStatus | '') {
  status.value = s
  void reload()
}

async function select(id: string) {
  await run(() => selectIncident(id))
}

async function openIncident(r: OpenIncidentRequest) {
  busy.value = true
  loadError.value = ''
  try {
    await open(r)
  } catch (e) {
    loadError.value = t('incidents.open.failed', { error: errorText(e) })
  } finally {
    busy.value = false
  }
}

async function toggleSettings() {
  settingsOpen.value = !settingsOpen.value
  if (settingsOpen.value && !settings.value) await run(loadSettings)
}

async function saveSettings(s: Settings) {
  await run(() => persistSettings(s), 'settings.saved')
}

function ask(title: string, action: () => void) {
  confirm.title = title
  confirm.action = action
  confirm.open = true
}

const askDelete = () => ask(t('incidents.detail.deleteConfirm'), () => void run(remove))
const askDeleteFragment = (id: string) => ask(t('incidents.evidence.deleteConfirm'), () => void run(() => deleteFragment(id)))
const askDeleteAll = () => ask(t('incidents.evidence.deleteAllConfirm'), () => void run(() => setLogCapture(detail.value?.incident.saveLogs ?? true, true)))

onMounted(() => {
  void reload()
  unlisten = listen()
})
onBeforeUnmount(() => unlisten?.())
</script>

<style scoped>
.incidents-view { padding: 24px; text-align: left; max-width: 1300px; display: flex; flex-direction: column; gap: 20px; }
.head { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; }
.head h2 { margin: 0; font-size: 22px; color: #89b4fa; }
.sub { margin: 4px 0 0; color: #a6adc8; font-size: 13px; }
.actions { display: flex; gap: 8px; }
.panel h3 { margin: 0 0 10px; font-size: 15px; color: #cdd6f4; }
</style>
