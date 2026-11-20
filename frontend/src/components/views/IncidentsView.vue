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
        @outcome="(v) => run(() => markOutcome(v))"
        @open-problem="openProblem" />
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
import { useIncidentProblems } from '../../composables/useIncidentProblems'
import { useNotifications } from '../../composables/useNotifications'
import ConfirmationDialog from '../utils/ConfirmationDialog.vue'
import IncidentDetail from '../incidents/IncidentDetail.vue'
import IncidentList from '../incidents/IncidentList.vue'
import IncidentSettings from '../incidents/IncidentSettings.vue'
import OpenIncidentForm from '../incidents/OpenIncidentForm.vue'
import ProblemList from '../incidents/ProblemList.vue'
import ProblemView from '../incidents/ProblemView.vue'
import type { IncidentSettings as Settings, IncidentStatus, OpenIncidentRequest, ProblemStatus } from '../../types/incident'

const { t } = useI18n()
const { activeClusterId } = useClusters()
const notifications = useNotifications()
const {
  items, nextCursor, status, loading, detail, settings, canReadLogs,
  reset, loadList, open, select: selectIncident, close, refreshEvents, markOutcome, saveFragment, deleteFragment, setLogCapture,
  readFragment, remove, exportIncident, loadSettings, saveSettings: persistSettings, listen,
} = useIncidents(() => activeClusterId.value)
