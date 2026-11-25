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
      <IncidentDetail :incident="detail.incident" :fragments="detail.fragments" :can-read-logs="canReadLogs"
        :read="readFragment" :busy="busy" :stats="stats" :previous="previous" :can-operate="canOperate" @back="close" @refresh="run(refreshEvents)" @recompute-correlation="run(recomputeCorrelation)" @export="(w) => run(() => exportIncident(w))" @delete="askDelete"
        @save-fragment="(ev) => run(() => saveFragment(ev.pod, ev.container, !!ev.previous), 'evidence.saveDone')"
        @delete-fragment="askDeleteFragment" @delete-all-fragments="askDeleteAll"
        @capture="(on) => run(() => setLogCapture(on, false))"
        @outcome="(v) => run(() => markOutcome(v))"
        @open-problem="openProblem" @open-incident="select" />
    </template>
    <template v-else-if="problemDetail">
      <ProblemView :problem="problemDetail" :can-operate="canOperate" :busy="busy" :attachable="attachable" :merge-targets="mergeTargets"
        @back="closeProblem" @open-incident="select" @attach="askAttach" @detach="askDetach" @merge="askMerge" />
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
      <section class="panel" data-testid="problems-panel">
        <h3>{{ $t('incidentsIntel.problem.title') }}</h3>
        <p class="sub">{{ $t('incidentsIntel.problem.subtitle') }}</p>
        <ProblemList :items="problemItems" :status="problemStatus" :loading="problemsLoading" :has-more="!!problemCursor" :can-operate="canOperate"
          :busy="busy" :candidates="attachable" @select="openProblem" @filter="setProblemFilter" @more="run(() => loadProblems(true))"
          @create="askCreate" />
      </section>
      <section class="panel" data-testid="stats-panel">
        <details>
          <summary class="panel-title">{{ $t('incidentsInsights.stats.title') }}</summary>
          <p class="sub">{{ $t('incidentsInsights.stats.subtitle') }}</p>
          <RuleStatsPanel :stats="stats" :loading="statsLoading" :can-export="canExport" :busy="busy" @export="askExport" />
        </details>
      </section>
    </template>

    <ConfirmationDialog v-model="confirm.open" resource-type="" resource-name="" :title="confirm.title" @confirm="confirm.action()" />
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import { useClusters } from '../../composables/useClusters'
import { useIncidents } from '../../composables/useIncidents'
import { useIncidentInsights } from '../../composables/useIncidentInsights'
import { useIncidentProblems } from '../../composables/useIncidentProblems'
import { useNotifications } from '../../composables/useNotifications'
import ConfirmationDialog from '../utils/ConfirmationDialog.vue'
import IncidentDetail from '../incidents/IncidentDetail.vue'
import IncidentList from '../incidents/IncidentList.vue'
import IncidentSettings from '../incidents/IncidentSettings.vue'
import OpenIncidentForm from '../incidents/OpenIncidentForm.vue'
import ProblemList from '../incidents/ProblemList.vue'
import ProblemView from '../incidents/ProblemView.vue'
import RuleStatsPanel from '../incidents/RuleStatsPanel.vue'
import type { IncidentSettings as Settings, IncidentStatus, OpenIncidentRequest, ProblemStatus } from '../../types/incident'

const { t } = useI18n()
const { activeClusterId } = useClusters()
const notifications = useNotifications()
const {
  items, nextCursor, status, loading, detail, settings, canReadLogs,
  reset, loadList, open, select: selectIncident, close, refreshEvents, recomputeCorrelation, markOutcome, saveFragment, deleteFragment, setLogCapture,
  readFragment, remove, exportIncident, loadSettings, saveSettings: persistSettings, listen,
} = useIncidents(() => activeClusterId.value)

const {
  items: problemItems, nextCursor: problemCursor, status: problemStatus, loading: problemsLoading, detail: problemDetail, canOperate,
  reset: resetProblems, loadList: loadProblems, select: selectProblem, close: closeProblem, attach, detach, merge, create, listen: listenProblems,
} = useIncidentProblems(() => activeClusterId.value)

const {
  stats, statsLoading, previous, canExport, reset: resetInsights, loadStats, loadPrevious, clearPrevious, exportStats,
} = useIncidentInsights(() => activeClusterId.value)

/** Расследования без проблемы: их можно присоединить или объединить в новую проблему. */
const attachable = computed(() => items.value.filter((i) => !i.problemId))
const mergeTargets = computed(() => problemItems.value.filter((p) => p.status === 'open' && p.id !== problemDetail.value?.id))

const busy = ref(false)
const loadError = ref('')
const settingsOpen = ref(false)
const confirm = reactive({ open: false, title: '', action: () => {} })
let unlisten: (() => void) | null = null
let unlistenProblems: (() => void) | null = null

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
  await run(() => loadProblems())
  void loadStats()
}

/** При повторении подгружаем предыдущее расследование для баннера; при смене расследования — сбрасываем. */
watch(() => detail.value?.incident.recurrenceOf ?? '', (id) => {
  if (id) void loadPrevious(id)
  else clearPrevious()
}, { immediate: true })

function setProblemFilter(s: ProblemStatus | '') {
  problemStatus.value = s
  void run(() => loadProblems())
}

/** Из расследования или списка — на экран проблемы; расследование закрывается, чтобы показать проблему. */
async function openProblem(id: string) {
  close()
  await run(() => selectProblem(id))
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

/** Действия над проблемой всегда с подтверждением и напоминанием: расследования не удаляются и не сливаются. */
const confirmText = (key: string) => `${t(`incidentsIntel.problem.confirm.${key}`)} ${t('incidentsIntel.problem.safeNote')}`
const askAttach = (incidentId: string) => ask(confirmText('attach'), () => void run(() => attach(incidentId)))
const askDetach = (incidentId: string) => ask(confirmText('detach'), () => void run(() => detach(incidentId)))
const askMerge = (toId: string) => ask(confirmText('merge'), () => void run(() => merge(toId)))
const askCreate = (ids: string[]) => ask(confirmText('create'), () => void run(() => create(ids)))

const askExport = () => ask(t('incidentsInsights.stats.exportConfirm'), () => void run(exportStats))

const askDelete = () => ask(t('incidents.detail.deleteConfirm'), () => void run(remove))
const askDeleteFragment = (id: string) => ask(t('incidents.evidence.deleteConfirm'), () => void run(() => deleteFragment(id)))
const askDeleteAll = () => ask(t('incidents.evidence.deleteAllConfirm'), () => void run(() => setLogCapture(detail.value?.incident.saveLogs ?? true, true)))

onMounted(() => {
  void reload()
  unlisten = listen()
  unlistenProblems = listenProblems()
})
// Смена активного кластера: список, открытое расследование и настройки прежнего кластера сбрасываются,
// ответы на его запросы отбрасываются (в composable), список читается заново для нового кластера
watch(activeClusterId, (id, prev) => {
  if (id === prev) return
  reset()
  resetProblems()
  resetInsights()
  loadError.value = ''
  settingsOpen.value = false
  confirm.open = false
  if (id) void reload()
})
onBeforeUnmount(() => {
  unlisten?.()
  unlistenProblems?.()
})
</script>

<style scoped>
.incidents-view { padding: 24px; text-align: left; max-width: 1300px; display: flex; flex-direction: column; gap: 20px; }
.head { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; }
.head h2 { margin: 0; font-size: 22px; color: #89b4fa; }
.sub { margin: 4px 0 0; color: #a6adc8; font-size: 13px; }
.actions { display: flex; gap: 8px; }
.panel h3 { margin: 0 0 10px; font-size: 15px; color: #cdd6f4; }
</style>
