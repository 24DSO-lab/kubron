// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref, shallowRef } from 'vue'
import { callApp } from './wails'
import { askConfirm } from './confirm'
import { reloadActive } from './store'
import { useNotifications } from '../composables/useNotifications'
import { useK8s } from '../composables/useK8s'
import { errorText } from '../composables/errorText'
import { t } from '../i18n'
import { openUrl } from '../platform/files'
import type {
  ArgoActionResult, ArgoAppRef, ArgoApplicationInfo, ArgoHistoryEntry, ArgoSyncOptions,
} from '../types/argocd'

/**
 * Действия над Argo CD Application. Отдельный модуль, как confirm.ts: определения ресурсов
 * импортируют его напрямую, а не через actions.ts (иначе registry -> definitions -> actions даст цикл).
 */
const notifications = useNotifications()

/** Диалог Sync (рисует ResourceHost). Одно приложение — обычный sync; несколько — массовый */
export const pendingArgoSync = shallowRef<{ apps: ArgoApplicationInfo[] } | null>(null)
/** Растёт после каждого действия: дашборд по нему перечитывает данные */
export const argoActionTick = ref(0)

export const requestArgoSync = (apps: ArgoApplicationInfo[]) => {
  if (apps.length) pendingArgoSync.value = { apps }
}
export const closeArgoSync = () => { pendingArgoSync.value = null }

export const refOf = (a: Pick<ArgoApplicationInfo, 'namespace' | 'name'>): ArgoAppRef => ({
  namespace: a.namespace,
  name: a.name,
})

/** Argo реагирует с задержкой: обновляем сразу и ещё раз через пару секунд */
function changed() {
  const bump = () => { argoActionTick.value++; void reloadActive() }
  bump()
  setTimeout(bump, 3000)
}

/** Тосты по результатам; true — всё прошло успешно */
function report(results: ArgoActionResult[], done: string): boolean {
  const failed = results.filter(r => r.error)
  const ok = results.length - failed.length
  if (ok) {
    notifications.notifySuccess(results.length === 1
      ? t('argo.actions.doneOne', { name: results[0].name, done })
      : t('argo.actions.doneMany', { done, ok, total: results.length }))
  }
  for (const f of failed.slice(0, 3)) notifications.notifyError(t('argo.actions.failedItem', { name: f.name, error: errorText(f.error) }))
  if (failed.length > 3) notifications.notifyError(t('argo.actions.moreFailed', { n: failed.length - 3 }))
  return failed.length === 0
}

export async function runArgoSync(apps: ArgoApplicationInfo[], opts: ArgoSyncOptions): Promise<void> {
  try {
    const res = await callApp('SyncArgoApplication', apps.map(refOf), opts)
    report(res, opts.dryRun ? t('argo.actions.dryRunStarted') : t('argo.actions.syncStarted'))
    changed()
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.sync', { error: errorText(e) }))
  }
}

export async function refreshArgoApps(apps: ArgoApplicationInfo[], hard = false): Promise<void> {
  try {
    const res = await callApp('RefreshArgoApplication', apps.map(refOf), hard)
    report(res, hard ? t('argo.actions.hardRefreshRequested') : t('argo.actions.refreshRequested'))
    changed()
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.refresh', { error: errorText(e) }))
  }
}

export async function terminateArgoOperation(apps: ArgoApplicationInfo[]): Promise<void> {
  const question = apps.length === 1
    ? t('argo.actions.terminateConfirmOne', { name: apps[0].name })
    : t('argo.actions.terminateConfirmMany', { n: apps.length })
  if (!(await askConfirm(question))) return
  try {
    const res = await callApp('TerminateArgoOperation', apps.map(refOf))
    report(res, t('argo.actions.terminating'))
    changed()
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.terminate', { error: errorText(e) }))
  }
}

export async function rollbackArgo(app: Pick<ArgoApplicationInfo, 'namespace' | 'name'>, entry: ArgoHistoryEntry): Promise<void> {
  if (!(await askConfirm(t('argo.actions.rollbackConfirm', { name: app.name, id: entry.id })))) return
  try {
    await callApp('RollbackArgoApplication', app.namespace, app.name, entry.id, false, false)
    notifications.notifySuccess(t('argo.actions.rollbackStarted', { name: app.name, id: entry.id }))
    changed()
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.rollback', { error: errorText(e) }))
  }
}

/** Порт-форвард на argocd-server и открытие в браузере (по самоподписанному сертификату браузер предупредит) */
export async function openArgoUI(): Promise<void> {
  try {
    const info = await callApp('OpenArgoUI')
    if (info.mode === 'external') {
      // Внешний Argo URL задаётся администратором команды. Показываем origin до перехода,
      // чтобы пользователь мог заметить неожиданный или подменённый домен.
      const host = new URL(info.url).host
      if (!(await askConfirm(t('argo.actions.externalUrlConfirm', { host })))) return
    } else {
      await useK8s().refreshPortForwards()
    }
    openUrl(info.url)
    // В режиме proxy адрес одноразовый и пользователю бесполезен: показываем, что именно открыто
    if (!info.reused) notifications.notifySuccess(t('argo.actions.uiOpened', { url: info.mode === 'proxy' ? `${info.namespace}/${info.service}` : info.url }))
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.ui', { error: errorText(e) }))
  }
}

/** Приложение, для которого открыт диалог удаления (рисует ResourceHost) */
export const pendingArgoDelete = shallowRef<ArgoApplicationInfo | null>(null)
export const requestArgoDelete = (app: ArgoApplicationInfo) => { pendingArgoDelete.value = app }
export const closeArgoDelete = () => { pendingArgoDelete.value = null }

/** true — удалено (вызывающий закрывает панель деталей) */
export async function deleteArgoApplication(app: ArgoApplicationInfo, cascade: boolean): Promise<boolean> {
  try {
    await callApp('DeleteArgoApplication', app.namespace, app.name, cascade)
    notifications.notifySuccess(t(cascade ? 'argo.actions.deletedCascade' : 'argo.actions.deletedKept', { name: app.name }))
    changed()
    return true
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.delete', { error: errorText(e) }))
    return false
  }
}

export async function setArgoAutoSync(apps: ArgoApplicationInfo[], enabled: boolean): Promise<void> {
  if (enabled) {
    const question = apps.length === 1
      ? t('argo.actions.autoSyncConfirmOne', { name: apps[0].name })
      : t('argo.actions.autoSyncConfirmMany', { n: apps.length })
    if (!(await askConfirm(question))) return
  }
  try {
    const res = await callApp(enabled ? 'ResumeArgoAutoSync' : 'SuspendArgoAutoSync', apps.map(refOf))
    report(res, enabled ? t('argo.actions.autoSyncEnabled') : t('argo.actions.autoSyncDisabled'))
    changed()
  } catch (e) {
    notifications.notifyError(t('argo.actions.error.autoSync', { error: errorText(e) }))
  }
}