// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref } from 'vue'
import { onEvent } from '../platform/events'

/** Событие бэкенда: объект Flux создан, изменён или удалён (informer, internal/k8s, EventFluxChanged) */
const FLUX_CHANGED = 'flux:changed'

/**
 * Счётчик изменений Flux. Определения ресурсов подписываются на него через `refreshOn`,
 * дашборд — через watch: сам список перезагружается существующим механизмом, без новых каналов.
 * Таймер autoRefresh остаётся страховкой: informer может не стартовать (нет прав на list/watch).
 */
export const fluxLiveTick = ref(0)

let off: (() => void) | null = null

/** Подписка идемпотентна: повторный вызов (смена кластера) не плодит обработчики */
export function startFluxLive() {
  if (off) return
  off = onEvent(FLUX_CHANGED, () => { fluxLiveTick.value++ })
}

export function stopFluxLive() {
  off?.()
  off = null
}
