// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref, shallowRef } from 'vue'
import { globalTabbedPanel } from '../composables/useTabbedPanel'
import { useNotifications } from '../composables/useNotifications'
import { getResourceById } from './registry'
import { reloadActive } from './store'
import { callApp } from './wails'
import { askConfirm } from './confirm'
import { runBatch } from './bulk'
import { manifestTemplate } from './templates'
import type { ActionContext } from './types'
import { can } from '../composables/useAuth'
import { Perm, type Permission } from '../types/auth'
import { t } from '../i18n'
import { errorText } from '../composables/errorText'

const notifications = useNotifications()

/** Какая панель деталей открыта: одна на всё приложение */
export const opened = shallowRef<{ id: string; ns: string; name: string; item?: any } | null>(null)

/** Ресурс, ожидающий подтверждения удаления */
export const pendingDelete = ref<{ id: string; kind: string; ns: string; name: string } | null>(null)

/** Объекты одного типа, ожидающие подтверждения массового удаления (чекбоксы + «−») */
export const pendingBulkDelete = ref<{
  id: string
  kind: string
  items: { ns: string; name: string }[]
} | null>(null)

/**
 * Объекты, на которые массовое удаление успешно отправлено. Под при удалении ещё какое-то время
 * (graceful termination) остаётся в списке, поэтому список по нему выбор не снимет: это делает ResourceList.
 */
export const bulkDeleted = shallowRef<{ id: string; items: { ns: string; name: string }[] } | null>(null)

/** Сколько имён с причиной ошибки показываем в тосте; остальные — счётчиком */
const MAX_FAILURES_SHOWN = 3

/** Deployment, для которого открыт диалог масштабирования */
export const scaleTarget = shallowRef<any | null>(null)

/** Под, для которого открыт диалог отладочного (ephemeral) контейнера */
export const debugTarget = shallowRef<{ namespace: string; pod: string; containers: string[]; target?: string } | null>(null)

/** Параметры, которые пользователь выбрал в диалоге отладочного контейнера */
export interface DebugStartParams { image: string; profile: string; target: string }

/** Отладочный контейнер, созданный бэкендом (Go: k8s.DebugContainer) */
interface DebugContainerInfo { namespace: string; pod: string; container: string }

/** Запасная проверка в самих действиях (кнопки без права и так скрыты); бэкенд проверяет независимо */
function allowed(permission: Permission): boolean {
  if (can(permission)) return true
  notifications.notifyError(t('actions.noPermission'))
  return false
}

export const resourceActions: ActionContext & {
  closeDetail(): void
  confirmDelete(): Promise<void>
  cancelDelete(): void
  requestBulkDelete(id: string, items: { ns: string; name: string }[]): void
  confirmBulkDelete(): Promise<void>
  cancelBulkDelete(): void
  /** Кнопка «+»: открыть в редакторе YAML базовый манифест нового объекта этого типа */
  create(id: string): void
  confirmScale(replicas: number): Promise<void>
  cancelScale(): void
  /** Создаёт отладочный контейнер и открывает к нему терминал (attach). Ошибку бросает диалогу: он показывает её и даёт повторить. */
  confirmDebug(params: DebugStartParams): Promise<void>
  cancelDebug(): void
} = {
  open(id, ns, name, item) {
    opened.value = { id: id.toLowerCase(), ns, name, item }
  },

  closeDetail() {
    opened.value = null
  },


  async edit(id, ns, name) {
    try {
      const yaml = await callApp('GetResourceYAML', ns, id, name, true)
      globalTabbedPanel.openYamlTab(id, name, ns, yaml)
    } catch (e) {
      notifications.notifyError(t('actions.loadYamlFailed', { error: errorText(e) }))
    }
  },

  requestDelete(id, ns, name) {
    if (!allowed(Perm.Write)) return
    const def = getResourceById(id)
    if (!def?.api.remove) {
      notifications.notifyError(t('actions.deleteUnsupported', { kind: def?.kind ?? id }))
      return
    }
    pendingDelete.value = { id: def.id, kind: def.kind, ns, name }
  },

  async confirmDelete() {
    const p = pendingDelete.value
    pendingDelete.value = null
    if (!p || !allowed(Perm.Write)) return
    const def = getResourceById(p.id)
    if (!def?.api.remove) return
    try {
      await def.api.remove(p.ns, p.name)
      notifications.notifySuccess(t('actions.deleted', { kind: p.kind, name: p.name }))
      const o = opened.value
      if (o && o.id === p.id && o.ns === p.ns && o.name === p.name) opened.value = null
      await reloadActive()
    } catch (e) {
      notifications.notifyError(t('actions.deleteFailed', { error: errorText(e) }))
    }
  },

  cancelDelete() {
    pendingDelete.value = null
  },

  requestBulkDelete(id, items) {
    if (!items.length || !allowed(Perm.Write)) return
    const def = getResourceById(id)
    if (!def?.api.remove) {
      notifications.notifyError(t('actions.deleteUnsupported', { kind: def?.kind ?? id }))
      return
    }
    pendingBulkDelete.value = { id: def.id, kind: def.kind, items }
  },

  async confirmBulkDelete() {
    const p = pendingBulkDelete.value
    pendingBulkDelete.value = null
    if (!p || !allowed(Perm.Write)) return
    const remove = getResourceById(p.id)?.api.remove
    if (!remove) return

    // Ошибка одного объекта не должна обрывать остальные
    const res = await runBatch(p.items, it => remove(it.ns, it.name))

    if (res.done.length) bulkDeleted.value = { id: p.id, items: res.done }

    const o = opened.value
    if (o && o.id === p.id && res.done.some(it => it.ns === o.ns && it.name === o.name)) opened.value = null

    if (res.done.length) {
      notifications.notifySuccess(
        res.done.length === 1 && !res.failed.length
          ? t('actions.deleted', { kind: p.kind, name: res.done[0].name })
          : t('actions.bulkDeleted', { kind: p.kind, done: res.done.length, total: p.items.length }),
      )
    }
    if (res.failed.length) {
      const shown = res.failed.slice(0, MAX_FAILURES_SHOWN).map(f => `${f.item.name}: ${f.error}`)
      const rest = res.failed.length - shown.length
      notifications.notifyError(
        t('actions.bulkFailed', { failed: res.failed.length, total: p.items.length, list: shown.join('; ') + (rest > 0 ? ' ' + t('actions.andMore', { n: rest }) : '') }),
      )
    }
    await reloadActive()
  },

  cancelBulkDelete() {
    pendingBulkDelete.value = null
  },

  create(id) {
    if (!allowed(Perm.Write)) return
    const def = getResourceById(id)
    const yaml = def ? manifestTemplate(def) : null
    if (!def || yaml === null) {
      notifications.notifyError(t('actions.createUnsupported', { kind: def?.kind ?? id }))
      return
    }
    globalTabbedPanel.openCreateYamlTab(def.id, def.kind, yaml)
  },

  openLogs(ns, pod, containers, container) {
    const list = containers?.length ? containers : container ? [{ name: container }] : []
    globalTabbedPanel.openLogsTab('pod', pod, ns, list, container ?? list[0]?.name)
  },

  // Предполагается, что TabbedPanel сам подгружает поды для не-pod ресурсов (LogsTab.pods = [])
  openResourceLogs(kind, ns, name) {
    globalTabbedPanel.openLogsTab(kind, name, ns)
  },

  async openShell(ns, pod, container) {
    if (!allowed(Perm.Exec)) return
    return globalTabbedPanel.openTerminalTab('pod', pod, ns, pod, container, 'shell')
  },

  async openAttach(ns, pod, container) {
    if (!allowed(Perm.Exec)) return
    return globalTabbedPanel.openTerminalTab('pod', pod, ns, pod, container, 'attach')
  },

  requestScale(item, kind) {
    if (!allowed(Perm.Operate)) return
    scaleTarget.value = kind ? { ...item, _kind: kind } : item
  },

  async confirmScale(replicas) {
    const target = scaleTarget.value
    scaleTarget.value = null
    if (!target) return
    const kind = target._kind || target.kind || 'Deployment'
    const method = kind === 'StatefulSet' ? 'ScaleStatefulSet' : kind === 'ReplicaSet' ? 'ScaleReplicaSet' : 'ScaleDeployment'
    await resourceActions.perform(
      () => callApp(method, target.namespace, target.name, replicas),
      t('actions.scaled', { kind, name: target.name, n: replicas }),
    )
  },

  cancelScale() {
    scaleTarget.value = null
  },

  requestDebug(ns, pod, containers, target) {
    // Подключение к отладочному контейнеру идёт терминалом (attach): без exec контейнер создался бы зря
    if (!allowed(Perm.Debug) || !allowed(Perm.Exec)) return
    debugTarget.value = { namespace: ns, pod, containers, target }
  },

  async confirmDebug({ image, profile, target }) {
    const dt = debugTarget.value
    if (!dt) return
    const dc = await callApp('StartDebugContainer', dt.namespace, dt.pod, target, image, profile) as DebugContainerInfo
    debugTarget.value = null
    notifications.notifySuccess(t('actions.debugStarted', { name: dc.container, pod: dt.pod }))
    // Новый контейнер виден в деталях пода; обновление списка не должно мешать открытию терминала
    void reloadActive()
    await globalTabbedPanel.openTerminalTab('pod', dt.pod, dt.namespace, dt.pod, dc.container, 'attach')
  },

  cancelDebug() {
    debugTarget.value = null
  },

  // Свой диалог вместо window.confirm: в Wails нативный confirm работает не на всех ОС
  confirm: askConfirm,

  async perform(fn, successMessage) {
    try {
      await fn()
      if (successMessage) notifications.notifySuccess(successMessage)
      await reloadActive()
    } catch (e) {
      notifications.notifyError(errorText(e))
    }
  },

  reload: () => reloadActive(),
  notifySuccess: m => notifications.notifySuccess(m),
  notifyError: m => notifications.notifyError(m),
}
