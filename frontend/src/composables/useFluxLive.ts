// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref } from 'vue'

/**
 * Заглушка задачи 0.2: подписку на событие flux:changed (internal/k8s, EventFluxChanged) заполняет задача U.2.
 * Определения ресурсов подписываются на счётчик через `refreshOn`, дашборд — через watch.
 */
export const fluxLiveTick = ref(0)

/** Подписка идемпотентна: повторный вызов (смена кластера) не плодит обработчики */
export function startFluxLive() {}

export function stopFluxLive() {}
