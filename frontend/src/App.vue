<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div id="app">
    <!-- Автовыход по бездействию: следит за вводом и предупреждает перед выходом -->
    <IdleGuard />

    <!-- Прогресс длинных Helm-операций (установка, откат, удаление) -->
    <HelmOpsPanel />

    <!-- Экран входа / первичной настройки / смены временного пароля -->
    <AuthScreen v-if="auth.screen.value !== 'app'" :mode="auth.screen.value" />

    <template v-else>
    <!-- Header -->
    <header class="header" :class="{ 'env-prod': activeClusterMeta?.environment === 'prod' }">
      <div class="header-brand">
        <img src="./assets/images/logo.svg" alt="Kubron" class="logo" />
      </div>
      <div v-if="activeClusterId && activeClusterMeta" class="connection-info">
        <ClusterAvatar :name="activeClusterMeta.displayName" :icon="activeClusterMeta.icon"
          :color="activeClusterMeta.color" :size="26" />
        <span class="active-cluster-name">{{ activeClusterMeta.displayName }}</span>
        <span v-if="activeEnvironment" class="env-badge"
          :style="{ color: activeEnvironment.color, borderColor: activeEnvironment.color }">
          {{ tl(activeEnvironment.label) }}
        </span>
        <button class="btn-disconnect" :title="$t('app.header.disconnect')" :aria-label="$t('app.header.disconnect')"
          @click="clusterConn.disconnect()">✕</button>
      </div>
      <div class="header-right">
        <VersionBadge />
        <BugReportButton :view="currentView" />
        <UserMenu />
      </div>
    </header>

    <!-- Вышла новая версия: баннер только администратору (ADR-0028) -->
    <UpdateBanner />

    <div class="app-body">
      <!-- Cluster Bar -->
      <ClusterBar :clusters="clusters" @select="clusterConn.selectCluster"
        @add="clusterConn.showAddCluster.value = true" @edit="clusterConn.openEditCluster"
        @delete="clusterConn.deleteCluster" />

      <!-- Разделы управления без кластера: команды, пользователи, журнал -->
      <div v-if="managementOpen" class="mgmt-screen" data-testid="management">
        <nav class="mgmt-tabs" :aria-label="$t('app.management.label')">
          <button v-for="m in mgmtViews" :key="m.view" type="button" class="mgmt-tab"
            :class="{ active: currentView === m.view }" @click="openManagement(m.view)">{{ $t(m.labelKey) }}</button>
          <span class="mgmt-spacer"></span>
          <button type="button" class="mgmt-tab" @click="closeManagement">{{ $t('app.management.back') }}</button>
        </nav>
        <main class="content">
          <component :is="activeViewComponent" v-if="activeViewComponent" />
        </main>
      </div>

      <!-- Нет кластеров -->
      <div v-else-if="clusters.length === 0 && !clusterConn.showAddCluster.value" class="welcome-screen">
        <h2>{{ $t('app.welcome.title') }}</h2>
        <template v-if="can(Perm.ClusterManage)">
          <p>{{ $t('app.welcome.addPrompt') }}</p>
          <button class="btn" @click="clusterConn.showAddCluster.value = true">{{ $t('app.welcome.addButton') }}</button>
        </template>
        <p v-else>{{ $t('app.welcome.noClusters') }}</p>
        <div v-if="mgmtViews.length" class="mgmt-links" data-testid="management-links">
          <button v-for="m in mgmtViews" :key="m.view" type="button" class="btn" @click="openManagement(m.view)">{{ $t(m.labelKey) }}</button>
        </div>
      </div>

      <!-- Не подключен, но кластеры есть -->
      <div v-else-if="!activeClusterId && clusters.length > 0 && !clusterConn.connecting.value" class="welcome-screen">
        <h2>{{ $t('app.select.title') }}</h2>
        <p>{{ $t('app.select.hint') }}</p>
        <div v-if="mgmtViews.length" class="mgmt-links" data-testid="management-links">
          <button v-for="m in mgmtViews" :key="m.view" type="button" class="btn" @click="openManagement(m.view)">{{ $t(m.labelKey) }}</button>
        </div>
      </div>

      <!-- Основной интерфейс -->
      <template v-else-if="activeClusterId">
        <Sidebar :currentView="currentView" :selectedNamespaces="selectedNamespaces" :namespaces="namespaces"
          :clusterName="activeClusterMeta?.displayName" @navigate="onNavigate" @nsChange="onNsChange" />

        <main class="content">
          <!-- Ресурсы нового каркаса (src/resources): список рисует ResourceList -->
          <ResourceList v-if="activeResource" :key="`${activeClusterId}/${activeResource.view}`"
            :def="activeResource" />

          <!-- Остальные view (старый механизм) -->
          <template v-else>
            <div v-if="loading" class="loading">⏳ {{ $t('common.loading') }}</div>
            <component v-else-if="activeViewComponent" :is="activeViewComponent" v-bind="activeViewProps"
              v-on="activeViewEvents" />
          </template>
        </main>
      </template>
    </div>

    <!-- ═══ Панели нового каркаса: одна панель деталей и один диалог удаления ═══ -->
    <ResourceHost />

    <!-- Вкладки: Logs / YAML / Terminal -->
    <TabbedPanel v-if="globalTabbedPanel.isOpen()" @close="globalTabbedPanel.closeAllTabs()"
      @apply-yaml="applyYAMLTabbed" />

    <AddClusterDialog v-if="clusterConn.showAddCluster.value" @close="clusterConn.showAddCluster.value = false"
      :pick-file="clusterConn.pickKubeconfigFile" :submit="clusterConn.addCluster" />

    <ClusterEditDialog v-if="clusterConn.editingCluster.value" :key="clusterConn.editingCluster.value.id"
      :cluster="clusterConn.editingCluster.value" :pick-file="clusterConn.pickKubeconfigFile"
      :save="clusterConn.saveCluster" @close="clusterConn.closeEditCluster" @delete="clusterConn.deleteFromEdit" />

    <UpdateDialog />

    <NotificationToast :notifications="notifications.notifications.value" @dismiss="notifications.dismiss" />

    <!-- Диалог входа в кластер (вызовы ConnectCluster) -->
    <ClusterAuthHost @required="onAuthRequired" />

    <!-- Connecting overlay: пока открыт диалог входа, не показываем -->
    <div v-if="clusterConn.connecting.value && !clusterAuth.challenge.value" class="overlay">
      <div class="spinner">⏳ {{ $t('app.connecting') }}</div>
    </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, onUnmounted, watch, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { listenListFailures } from './composables/useListFailures'
import { listenClusterEvents } from './composables/useClusterEvents'
import { errorText } from './composables/errorText'
import { tl } from './i18n'
import { useK8s } from './composables/useK8s'
import { useClusters } from './composables/useClusters'
import { useClusterConnection } from './composables/useClusterConnection'
import { useNotifications } from './composables/useNotifications'
import { useAppSettings } from './composables/useAppSettings'
import { viewRegistry } from './composables/useViewRegistry'
import { globalTabbedPanel } from './composables/useTabbedPanel'
import { getResourceByView, getResourceById } from './resources/registry'
import { resourceActions } from './resources/actions'
import { DEFAULT_NAMESPACE } from './resources/templates'
import { clusterNameKey } from './resources/context'
import { clearResourceStore, reloadActive } from './resources/store'
import { answerConfirm } from './resources/confirm'
import { can, useAuth } from './composables/useAuth'
import { Perm } from './types/auth'
import { isManagementView, managementViews, type ManagementViewId } from './composables/managementViews'
import { useCapabilities } from './composables/useCapabilities'

import AuthScreen from './components/auth/AuthScreen.vue'
import UserMenu from './components/auth/UserMenu.vue'
import VersionBadge from './components/update/VersionBadge.vue'
import UpdateBanner from './components/update/UpdateBanner.vue'
import UpdateDialog from './components/update/UpdateDialog.vue'
import { useUpdates } from './composables/useUpdates'
import BugReportButton from './components/bugreport/BugReportButton.vue'
import IdleGuard from './components/auth/IdleGuard.vue'
import HelmOpsPanel from './components/HelmOpsPanel.vue'
import ClusterBar from './components/ClusterBar.vue'
import AddClusterDialog from './components/AddClusterDialog.vue'
import ClusterEditDialog from './components/cluster/ClusterEditDialog.vue'
import ClusterAvatar from './components/cluster/ClusterAvatar.vue'
import { findEnvironment } from './components/cluster/clusterStyle'
import Sidebar from './components/Sidebar.vue'
import NotificationToast from './components/NotificationToast.vue'
import ClusterAuthHost from './components/credential/ClusterAuthHost.vue'
import { AuthCancelledError, useClusterAuth } from './composables/useClusterAuth'
import ResourceList from './components/resource/ResourceList.vue'
import ResourceHost from './components/resource/ResourceHost.vue'
import TabbedPanel from './components/utils/TabbedPanel.vue'
import { navigationRequest } from './resources/customResources'
import { clearHelmContentCache } from './resources/helmContent'

// ═══════════════════════════════════════════════
// Composables
// ═══════════════════════════════════════════════

const { t } = useI18n()
const auth = useAuth()
const k8s = useK8s()
const clustersApi = useClusters()
const notifications = useNotifications()
const clusterAuth = useClusterAuth()

const {
  namespaces, selectedNamespaces, selectNamespaces, restoreNamespaces, currentView, loading,
  switchView, refreshData,
  loadNamespaces,
} = k8s

const {
  clusters, activeClusterId,
  loadClusters, connectToCluster, pickKubeconfigFile, addClusterFromFile, addClusterFromText, addInCluster,
  deleteCluster, updateCluster, autoConnect,
} = clustersApi

const clusterConn = useClusterConnection({
  clusters,
  activeClusterId,
  namespaces,
  currentView,
  connectToCluster,
  loadNamespaces,
  restoreNamespaces,
  refreshData,
  k8sDisconnect: k8s.disconnect,
  loadClusters,
  pickKubeconfigFile,
  addClusterFromFile,
  addClusterFromText,
  addInCluster,
  deleteCluster,
  updateCluster,
  autoConnect,
})
const activeClusterMeta = clusterConn.activeClusterMeta
const activeEnvironment = computed(() => findEnvironment(activeClusterMeta.value?.environment ?? ''))

// ═══════════════════════════════════════════════
// Для вкладок и секций нового каркаса (метрики и т.п.)
// ═══════════════════════════════════════════════

provide(clusterNameKey, activeClusterId)

function closeAllDetailPanels() {
  resourceActions.closeDetail()
}

watch(activeClusterId, () => {
  clearResourceStore()
  clearHelmContentCache()
  answerConfirm(false)
  closeAllDetailPanels()
})

// ═══════════════════════════════════════════════
// Обновление данных
// ═══════════════════════════════════════════════

/** После изменений: обновить и старый стор (если view старый), и новый (если открыт ResourceList) */
const refreshAll = () => Promise.all([refreshData(), reloadActive()])

// ═══════════════════════════════════════════════
// Навигация и открытие ресурсов по ссылке
// ═══════════════════════════════════════════════

function onNavigate(view: string) {
  closeAllDetailPanels()
  switchView(view)
}

function onNsChange(ns: string[]) {
  selectNamespaces(ns)
}

/** Переход к другому ресурсу из панели (Pod -> ReplicaSet, RoleBinding -> Role, ...) */
function onOpenResource(data: { kind: string; name: string; namespace?: string }) {
  let { kind, name } = data
  const namespace = data.namespace ?? ''

  // Допускаем форму "ReplicaSet/my-name" в поле name
  const parts = name.split('/')
  if (parts.length === 2) [kind, name] = parts

  closeAllDetailPanels()

  // Любой тип из resources/registry.ts открывается без доп. кода
  const def = getResourceById(kind)
  if (def) return resourceActions.open(def.id, namespace, name)

  notifications.notifyError(t('app.errors.unknownResource', { kind }))
}

// ═══════════════════════════════════════════════
// Динамический view
// ═══════════════════════════════════════════════

// Разделы управления открываются и без кластера: боковое меню есть только при подключении к кластеру,
// а свежему администратору организации нужно сначала создать команду и добавить кластер
const { capabilities: caps } = useCapabilities()
const mgmtViews = computed(() => managementViews({
  teams: caps.value.teams, users: can(Perm.UsersManage), audit: can(Perm.AuditRead),
}))
const managementOpen = computed(() =>
  !activeClusterId.value && isManagementView(currentView.value) && mgmtViews.value.some(m => m.view === currentView.value))

function openManagement(view: ManagementViewId) {
  closeAllDetailPanels()
  currentView.value = view // без обновления данных: кластера нет
}

function closeManagement() {
  currentView.value = 'overview'
}

const activeResource = computed(() => getResourceByView(currentView.value))

const activeViewConfig = computed(() => viewRegistry[currentView.value] || null)
const activeViewComponent = computed(() => activeViewConfig.value?.component || null)

const activeViewProps = computed(() => {
  const view = currentView.value
  const props: Record<string, any> = {}

  if (view === 'overview') {
    props.clusterName = activeClusterId.value
  }
  if (view === 'argo-dashboard') {
    props.clusterName = activeClusterId.value
  }
  if (view === 'flux-dashboard') {
    props.clusterName = activeClusterId.value
  }
  if (view === 'settings') {
    props.clusterName = activeClusterId.value
  }
  return props
})

/** Действия view -> обработчики. Это единственное место, где view связываются с логикой */
const viewHandlers: Record<string, () => Record<string, (...args: any[]) => void>> = {
  overview: () => ({ navigate: onNavigate }),
  'argo-dashboard': () => ({ navigate: onNavigate }),
  'flux-dashboard': () => ({ navigate: onNavigate }),
}

const activeViewEvents = computed(() => ({
  refresh: refreshData,
  ...viewHandlers[currentView.value]?.(),
}))

// ═══════════════════════════════════════════════
// YAML из TabbedPanel
// ═══════════════════════════════════════════════

async function applyYAMLTabbed(tabId: string) {
  if (!can(Perm.Write)) {
    notifications.notifyError(t('app.errors.noWritePermission'))
    return
  }
  const tab = globalTabbedPanel.tabs.value.find(t => t.id === tabId)
  if (!tab || tab.type !== 'yaml' || tab.saving) return
  tab.saving = true
  const applied = tab.content
  try {
    // Кнопка «+»: вкладка с новым манифестом создаёт объект, а не заменяет существующий
    if (tab.mode === 'create') {
      const name = await k8s.createResourceYAML(applied, tab.resourceType, DEFAULT_NAMESPACE)
      notifications.notifySuccess(t('app.yaml.created', { kind: tab.kindLabel ?? tab.resourceType, name }))
      globalTabbedPanel.closeTab(tab.id)
      await refreshAll()
      return
    }
    await k8s.applyYAML(applied, tab.resourceType, tab.namespace, tab.resourceName)
    tab.originalContent = applied
    notifications.notifySuccess(t('app.yaml.updated', { type: tab.resourceType, name: tab.resourceName }))
    await refreshAll()
  } catch (e: any) {
    notifications.notifyError(t(tab.mode === 'create' ? 'app.yaml.createError' : 'app.yaml.applyError', { message: errorText(e) }))
  } finally {
    tab.saving = false
  }
}

// ═══════════════════════════════════════════════
// Init
// ═══════════════════════════════════════════════

const appSettings = useAppSettings()
const updates = useUpdates()

// Данные кластеров загружаем только после входа: до этого бэкенд отвечает «требуется вход»
let appStarted = false
watch(
  () => auth.screen.value,
  screen => {
    if (screen !== 'app' || appStarted) return
    appStarted = true
    appSettings.loadSettings()
    clusterConn.initialize()
  },
  { immediate: true },
)

// Версия и обновления: читаем после входа (права нужны для состояния обновлений), при выходе сбрасываем
watch(
  () => auth.screen.value,
  screen => {
    if (screen === 'app') void updates.start()
    else updates.stop()
  },
  { immediate: true },
)

watch(navigationRequest, view => {
  if (!view) return
  navigationRequest.value = null
  onNavigate(view)
})

onMounted(() => auth.init())

// Бэкенд просит войти заново (истёк сохранённый вход): показываем диалог и обновляем данные
async function onAuthRequired(clusterId: string) {
  if (clusterId !== activeClusterId.value) return
  try {
    await clusterAuth.connect(clusterId)
    await refreshData()
  } catch (e) {
    if (!(e instanceof AuthCancelledError)) notifications.notifyError(t('cluster.connectionError', { message: errorText(e) }))
  }
}

// Бэкенд сообщает, если часть namespace набора прочитать не удалось (RBAC и т.п.)
const stopListFailures = listenListFailures()
onUnmounted(stopListFailures)

// Состояние соединения кластеров и потеря доступа к активному кластеру (сервисный режим)
const stopClusterEvents = listenClusterEvents({
  clusters, activeClusterId, loadClusters,
  disconnect: clusterConn.disconnect,
  notifyError: notifications.notifyError,
})
onUnmounted(stopClusterEvents)
</script>

<style src="./assets/style.css"></style>
