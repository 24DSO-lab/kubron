// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref } from 'vue'
import type {
  ClusterMeta, ClusterSettings, ClusterStatus, ClusterUpdateResult, KubeconfigSource, Outcome,
} from '../types'
import { askConfirm } from '../resources/confirm'
import { errorText } from './errorText'
import { callApp } from '../platform/api'
import { AuthCancelledError, useClusterAuth } from './useClusterAuth'
import { t } from '../i18n'

const clusters = ref<ClusterStatus[]>([])
const activeClusterId = ref('')
const showAddCluster = ref(false)
const clusterError = ref('')

async function loadClusters() {
  try {
    clusters.value = ((await callApp('GetClusterStatuses')) as ClusterStatus[]) || []
  } catch (e: any) {
    clusters.value = []
  }
}

/** Подключение через ConnectCluster: если нужен вход, показывается диалог вызова и ожидается его завершение. */
async function openConnection(id: string) {
  await useClusterAuth().connect(id)
}

async function connectToCluster(id: string) {
  try {
    clusterError.value = ''
    await openConnection(id)
    activeClusterId.value = id
    await loadClusters()
  } catch (e: any) {
    // Отмена входа пользователем — не ошибка
    if (!(e instanceof AuthCancelledError)) clusterError.value = t('cluster.errorPrefix', { message: errorText(e) })
    throw e
  }
}

async function pickKubeconfigFile(): Promise<string> {
  try {
    return (await callApp('PickKubeconfigFile', t('cluster.pickKubeconfigTitle'))) || ''
  } catch (e: any) {
    clusterError.value = errorText(e)
    return ''
  }
}

// Добавление и правка возвращают Outcome: диалог показывает ошибку у себя, не теряя введённое
async function addClusterFromFile(path: string, settings: ClusterSettings): Promise<Outcome<ClusterMeta>> {
  try {
    clusterError.value = ''
    const cluster = await callApp('AddClusterFromFile', path, settings)
    await loadClusters()
    return { ok: true, value: cluster }
  } catch (e) {
    clusterError.value = errorText(e)
    return { ok: false, error: clusterError.value }
  }
}

async function addClusterFromText(text: string, settings: ClusterSettings, teamId?: string): Promise<Outcome<ClusterMeta>> {
  try {
    clusterError.value = ''
    // В сервисном режиме кластер добавляется в выбранную команду
    let cluster: ClusterMeta
    if (teamId) {
      cluster = await callApp('AddClusterToTeam', teamId, text, settings)
    } else {
      cluster = await callApp('AddClusterFromText', text, settings)
    }
    await loadClusters()
    return { ok: true, value: cluster }
  } catch (e) {
    clusterError.value = errorText(e)
    return { ok: false, error: clusterError.value }
  }
}

async function addInCluster(settings: ClusterSettings, teamId: string): Promise<Outcome<ClusterMeta>> {
  try {
    clusterError.value = ''
    const cluster = await callApp('AddInClusterToTeam', teamId, settings)
    await loadClusters()
    return { ok: true, value: cluster }
  } catch (e) {
    clusterError.value = errorText(e)
    return { ok: false, error: clusterError.value }
  }
}

async function deleteCluster(id: string) {
  // Свой диалог вместо window.confirm: в Wails нативный confirm работает не на всех ОС
  const name = clusters.value.find(c => c.id === id)?.displayName ?? id
  if (!(await askConfirm(t('cluster.deleteConfirm', { name })))) return
  try {
    await callApp('DeleteCluster', id)
    if (activeClusterId.value === id) {
      activeClusterId.value = ''
    }
    await loadClusters()
  } catch (e) {
    clusterError.value = errorText(e)
  }
}

async function updateCluster(
  id: string, settings: ClusterSettings, kubeconfig: KubeconfigSource,
): Promise<Outcome<ClusterUpdateResult>> {
  try {
    clusterError.value = ''
    const result = await callApp('UpdateCluster', id, settings, kubeconfig)
    await loadClusters()
    return { ok: true, value: result }
  } catch (e) {
    clusterError.value = errorText(e)
    return { ok: false, error: clusterError.value }
  }
}

async function autoConnect() {
  try {
    const lastId = await callApp('GetLastActiveCluster')
    if (lastId) {
      const exists = clusters.value.find(c => c.id === lastId)
      if (exists) {
        await connectToCluster(lastId)
        return true
      }
    }
  } catch (e) {}
  return false
}

export function useClusters() {
  return {
    clusters,
    activeClusterId,
    showAddCluster,
    clusterError,
    loadClusters,
    connectToCluster,
    pickKubeconfigFile,
    addClusterFromFile,
    addClusterFromText,
    addInCluster,
    deleteCluster,
    updateCluster,
    autoConnect,
  }
}