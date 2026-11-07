// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// composables/useClusterConnection.ts

import { ref, computed, type Ref } from 'vue'
import { useArgo } from './useArgo'
import { useFlux } from './useFlux'
import { useNotifications } from './useNotifications'
import { errorText } from './errorText'
import { AuthCancelledError } from './useClusterAuth'
import { t } from '../i18n'
import { loadCustomResources, resetCustomResources } from '../resources/customResources'
import type {
  AddClusterPayload, ClusterMeta, ClusterSettings, ClusterStatus, ClusterUpdateResult,
  KubeconfigSource, NamespaceInfo, Outcome,
} from '../types'


export function useClusterConnection(deps: {
  clusters: Ref<ClusterStatus[]>
  activeClusterId: Ref<string>
  namespaces: Ref<NamespaceInfo[]>
  currentView: Ref<string>
  connectToCluster: (id: string) => Promise<void>
  loadNamespaces: () => Promise<void>
  restoreNamespaces: (clusterId: string, defaultNamespace: string) => void
  refreshData: () => Promise<void>
  k8sDisconnect: () => void
  loadClusters: () => Promise<void>
  pickKubeconfigFile: () => Promise<string>
  addClusterFromFile: (path: string, settings: ClusterSettings) => Promise<Outcome<ClusterMeta>>
  addClusterFromText: (text: string, settings: ClusterSettings, teamId?: string) => Promise<Outcome<ClusterMeta>>
  addInCluster: (settings: ClusterSettings, teamId: string) => Promise<Outcome<ClusterMeta>>
  deleteCluster: (id: string) => Promise<void>
  updateCluster: (id: string, settings: ClusterSettings, kubeconfig: KubeconfigSource) => Promise<Outcome<ClusterUpdateResult>>
  autoConnect: () => Promise<boolean>
}) {
  const {
    clusters, activeClusterId,
    namespaces, currentView,
    connectToCluster, loadNamespaces, restoreNamespaces, refreshData,
    k8sDisconnect, loadClusters,
    pickKubeconfigFile, addClusterFromFile, addClusterFromText, addInCluster,
    deleteCluster, updateCluster, autoConnect,
  } = deps

  const { refreshArgo, resetArgo } = useArgo()
  const { refreshFlux, resetFlux } = useFlux()
  const notifications = useNotifications()
  const connecting = ref(false)
  const showAddCluster = ref(false)
  const editingClusterId = ref<string | null>(null)

  const activeClusterMeta = computed(() =>
    clusters.value.find(c => c.id === activeClusterId.value) || null
  )

  // Кластер, открытый в диалоге правки; исчезает вместе с диалогом, если кластер удалили
  const editingCluster = computed(() =>
    clusters.value.find(c => c.id === editingClusterId.value) || null
  )

  /**
   * Выбор namespace после подключения: сохранённый для кластера, иначе namespace по умолчанию
   * из настроек кластера (если он есть в кластере), иначе «все».
   */
  function applyDefaultNamespace(clusterId: string) {
    restoreNamespaces(clusterId, clusters.value.find(c => c.id === clusterId)?.defaultNamespace ?? '')
  }

  async function selectCluster(id: string) {
    if (activeClusterId.value === id) return
    try {
      connecting.value = true
      await connectToCluster(id)
      await loadNamespaces()

      applyDefaultNamespace(id)
      currentView.value = 'overview'
       resetCustomResources()
      void loadCustomResources()
      void refreshArgo()
      void refreshFlux()
      await refreshData()
    } catch (e: any) {
      if (!(e instanceof AuthCancelledError)) notifications.notifyError(t('cluster.connectionError', { message: errorText(e) }))
    } finally {
      connecting.value = false
    }
  }

  /** Добавляет кластер и подключается к нему. Возвращает текст ошибки или null */
  async function addCluster(payload: AddClusterPayload): Promise<string | null> {
    let result: Outcome<ClusterMeta>
    if (payload.kind === 'file') result = await addClusterFromFile(payload.path, payload.settings)
    else if (payload.kind === 'in-cluster') result = await addInCluster(payload.settings, payload.teamId)
    else result = await addClusterFromText(payload.text, payload.settings, payload.teamId)
    if (!result.ok) return result.error
    showAddCluster.value = false
    await selectCluster(result.value.id)
    return null
  }

  function openEditCluster(id: string) {
    editingClusterId.value = id
  }

  function closeEditCluster() {
    editingClusterId.value = null
  }

  /**
   * Сохраняет правку кластера. Если у активного кластера сменился kubeconfig или контекст,
   * подключение пересоздаётся. Возвращает текст ошибки или null.
   */
  async function saveCluster(
    id: string, settings: ClusterSettings, kubeconfig: KubeconfigSource,
  ): Promise<string | null> {
    const result = await updateCluster(id, settings, kubeconfig)
    if (!result.ok) return result.error
    editingClusterId.value = null
    if (result.value.connectionChanged && activeClusterId.value === id) {
      // Бэкенд уже забыл старое подключение: переподключаемся как при обычном выборе кластера
      activeClusterId.value = ''
      await selectCluster(id)
    }
    return null
  }

  /** Удаление из диалога правки: сначала закрываем диалог, чтобы подтверждение не оказалось под ним */
  async function deleteFromEdit(id: string) {
    editingClusterId.value = null
    await deleteCluster(id)
  }

  function disconnect() {
    k8sDisconnect()
    resetArgo()
    resetFlux()
    resetCustomResources()
    activeClusterId.value = ''
    loadClusters()
  }

  async function initialize() {
    await loadClusters()
    const connected = await autoConnect()
    if (connected) {
      await loadNamespaces()
      applyDefaultNamespace(activeClusterId.value)
      currentView.value = 'overview'
      void refreshArgo()
      void refreshFlux()
      void loadCustomResources()
      await refreshData()
    }
  }

  return {
    connecting,
    showAddCluster,
    editingCluster,
    activeClusterMeta,
    selectCluster,
    pickKubeconfigFile,
    addCluster,
    openEditCluster,
    closeEditCluster,
    saveCluster,
    deleteFromEdit,
    disconnect,
    deleteCluster,
    initialize,
  }
}
