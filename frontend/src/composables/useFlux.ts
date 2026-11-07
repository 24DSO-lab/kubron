// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import type { FluxStatus } from '../types/flux'

/**
 * Заглушка задачи 0.2: обнаружение Flux (GetFluxStatus) и подписку на события заполняет задача U.2.
 * Пока Flux «не установлен», меню скрыто, приложение работает как раньше.
 */
const emptyStatus = (): FluxStatus => ({
  installed: false,
  state: '',
  controllers: { kustomize: false, helm: false, source: false, notification: false, image: false },
  versions: {},
})

/** Состояние Flux в активном кластере; от него зависит видимость меню (Sidebar) */
const fluxStatus = ref<FluxStatus>(emptyStatus())
const fluxInstalled = computed(() => fluxStatus.value.installed)

/** Перепроверяет кластер; ошибка (нет прав, кластер недоступен) = «не установлен» */
async function refreshFlux(): Promise<void> {}

function resetFlux() {
  fluxStatus.value = emptyStatus()
}

export function useFlux() {
  return { fluxInstalled, fluxStatus, refreshFlux, resetFlux }
}
