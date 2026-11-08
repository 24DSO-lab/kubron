// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref, shallowRef } from 'vue'
import { callApp } from './wails'
import { askConfirm } from './confirm'
import { reloadActive } from './store'
import { useNotifications } from '../composables/useNotifications'
import { errorText } from '../composables/errorText'
import { t } from '../i18n'
import type { FluxActionResult, FluxHelmReleaseInfo, FluxKustomizationInfo, FluxObjectInfo, FluxRef } from '../types/flux'

/**
 * Действия над объектами Flux. Отдельный модуль, как argoActions.ts: определения ресурсов и ResourceHost
 * импортируют его напрямую, а не через actions.ts (иначе registry -> definitions -> actions даст цикл).
 */
const notifications = useNotifications()

/** Растёт после каждого действия: дашборд по нему перечитывает данные */
export const fluxActionTick = ref(0)

/** Объект, для которого открыт диалог удаления (рисует ResourceHost); удалять можно только Kustomization и HelmRelease */
export const pendingFluxDelete = shallowRef<FluxKustomizationInfo | FluxHelmReleaseInfo | null>(null)
export const requestFluxDelete = (obj: FluxKustomizationInfo | FluxHelmReleaseInfo) => { pendingFluxDelete.value = obj }
export const closeFluxDelete = () => { pendingFluxDelete.value = null }

export const refOf = (o: Pick<FluxObjectInfo, 'kind' | 'namespace' | 'name'>): FluxRef => ({
  kind: o.kind,
  namespace: o.namespace,
  name: o.name,
})

/** Flux реагирует с задержкой: обновляем сразу и ещё раз через пару секунд */
function changed() {
  const bump = () => { fluxActionTick.value++; void reloadActive() }
  bump()
  setTimeout(bump, 3000)
}

/** Сколько имён с причиной ошибки показываем в тосте; остальные — счётчиком */
const MAX_FAILURES_SHOWN = 3

/** Тосты по результатам; true — всё прошло успешно. Объект без результата в ответе считается невыполненным */
export function reportFlux(items: Pick<FluxObjectInfo, 'name'>[], results: FluxActionResult[] | null | undefined, done: string): boolean {
  const res = results ?? []
  const failed = res.filter(r => r.error)
  const ok = res.length - failed.length
  if (ok > 0) {
    notifications.notifySuccess(res.length === 1
      ? t('fluxActions.doneOne', { name: res[0].name, done })
      : t('fluxActions.doneMany', { done, ok, total: res.length }))
  }
  for (const f of failed.slice(0, MAX_FAILURES_SHOWN)) {
    notifications.notifyError(t('fluxActions.failedItem', { name: f.name, error: errorText(f.error) }))
  }
  if (failed.length > MAX_FAILURES_SHOWN) notifications.notifyError(t('fluxActions.moreFailed', { n: failed.length - MAX_FAILURES_SHOWN }))
  const missing = items.length - res.length
  if (missing > 0) notifications.notifyError(t('fluxActions.noResult', { n: missing }))
  return failed.length === 0 && missing <= 0
}

/** reconcile объектов; withSource — вместе с источником */
export async function reconcileFlux(items: FluxObjectInfo[], opts: { withSource: boolean }): Promise<void> {
  if (!items.length) return
  try {
    const res = await callApp('ReconcileFlux', items.map(refOf), opts.withSource)
    reportFlux(items, res, opts.withSource ? t('fluxActions.reconcileWithSourceRequested') : t('fluxActions.reconcileRequested'))
    changed()
  } catch (e) {
    notifications.notifyError(t('fluxActions.error.reconcile', { error: errorText(e) }))
  }
}

/** Вопрос подтверждения suspend и resume; у suspend — предупреждение про Git */
export function suspendQuestion(items: Pick<FluxObjectInfo, 'name'>[], suspended: boolean): string {
  const key = `fluxActions.${suspended ? 'suspend' : 'resume'}Confirm${items.length === 1 ? 'One' : 'Many'}`
  return t(key, { name: items[0]?.name ?? '', n: items.length })
}

/** suspend (true) и resume (false) объектов, с подтверждением */
export async function setFluxSuspended(items: FluxObjectInfo[], suspended: boolean): Promise<void> {
  if (!items.length) return
  if (!(await askConfirm(suspendQuestion(items, suspended)))) return
  try {
    const refs = items.map(refOf)
    const res = suspended ? await callApp('SuspendFlux', refs) : await callApp('ResumeFlux', refs)
    reportFlux(items, res, suspended ? t('fluxActions.suspended') : t('fluxActions.resumed'))
    changed()
  } catch (e) {
    notifications.notifyError(t(suspended ? 'fluxActions.error.suspend' : 'fluxActions.error.resume', { error: errorText(e) }))
  }
}

// --- удаление ---------------------------------------------------------------------------------------

/** Что произойдёт при удалении: считается только из текущего состояния объекта (ADR-0023, раздел 6) */
export type FluxDeleteConsequence =
  | { kind: 'prune'; count: number }
  | { kind: 'keep' }
  | { kind: 'helmUninstall' }
  | { kind: 'none' }

export function deleteConsequence(obj: Partial<FluxKustomizationInfo> & Partial<FluxHelmReleaseInfo>): FluxDeleteConsequence {
  if (obj.kind === 'HelmRelease') return { kind: 'helmUninstall' }
  if (obj.kind === 'Kustomization') {
    // prune не задан или не true: как у Flux по умолчанию это «не удалять», но показываем осторожный вариант
    return obj.prune === true ? { kind: 'prune', count: Math.max(0, obj.inventoryCount ?? 0) } : { kind: 'keep' }
  }
  return { kind: 'none' }
}

/** Ключ i18n и параметры для последствия */
export function deleteConsequenceText(c: FluxDeleteConsequence): { key: string; params: Record<string, number> } | null {
  switch (c.kind) {
    case 'prune': return { key: 'fluxActions.delete.prune', params: { n: c.count } }
    case 'keep': return { key: 'fluxActions.delete.keep', params: {} }
    case 'helmUninstall': return { key: 'fluxActions.delete.helmUninstall', params: {} }
    default: return null
  }
}

/** Подтверждение — точное совпадение введённого текста с именем объекта (без обрезки пробелов) */
export const deleteConfirmed = (typed: string, name: string): boolean => name !== '' && typed === name

/** true — удалено (вызывающий закрывает панель деталей) */
export async function deleteFluxObject(obj: FluxKustomizationInfo | FluxHelmReleaseInfo): Promise<boolean> {
  try {
    await callApp('DeleteFlux', refOf(obj))
    notifications.notifySuccess(t('fluxActions.deleted', { name: obj.name }))
    changed()
    return true
  } catch (e) {
    notifications.notifyError(t('fluxActions.error.delete', { error: errorText(e) }))
    return false
  }
}
