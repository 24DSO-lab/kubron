// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Запуск расследования из карточки ресурса. Карточка кладёт цель сюда и переключает экран на «Расследования»;
 * экран забирает цель при монтировании и открывает расследование. Состояние общее на приложение (как useK8s).
 */
import { shallowRef } from 'vue'
import type { OpenIncidentRequest, TargetKind } from '../types/incident'
import { isClusterScoped } from '../types/incident'
import { useK8s } from './useK8s'

/** Цель, ожидающая открытия экраном расследований. */
export const pendingInvestigation = shallowRef<OpenIncidentRequest | null>(null)

/** Запоминает цель и переключает экран на «Расследования». У кластерных объектов namespace пустой. */
export function investigate(kind: TargetKind, namespace: string, name: string): void {
  pendingInvestigation.value = { kind, namespace: isClusterScoped(kind) ? '' : namespace, name, disableLogs: false }
  useK8s().switchView('incidents')
}

/** Забирает ожидающую цель (однократно): повторный вызов вернёт null. */
export function takeInvestigation(): OpenIncidentRequest | null {
  const r = pendingInvestigation.value
  pendingInvestigation.value = null
  return r
}
