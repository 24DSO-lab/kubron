// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import { callApp } from '../resources/wails'
import type { FluxControllers, FluxStatus, FluxStatusState } from '../types/flux'
import { startFluxLive, stopFluxLive } from './useFluxLive'

const emptyControllers = (): FluxControllers => ({
  kustomize: false, helm: false, source: false, notification: false, image: false,
})

const emptyStatus = (): FluxStatus => ({
  installed: false,
  state: '',
  controllers: emptyControllers(),
  versions: {},
})

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

/**
 * Защитный разбор ответа GetFluxStatus: привязки Wails отдают классы Go, а CRD живёт в чужих версиях,
 * поэтому отсутствующее или неожиданное поле даёт нулевое значение, а не исключение в интерфейсе.
 */
export function normalizeFluxStatus(raw: unknown): FluxStatus {
  if (!isRecord(raw)) return emptyStatus()
  const status = emptyStatus()
  status.installed = raw.installed === true
  const state = raw.state
  status.state = state === 'ok' || state === 'unsupportedVersion' ? (state satisfies FluxStatusState) : ''
  if (isRecord(raw.controllers)) {
    for (const key of Object.keys(status.controllers) as (keyof FluxControllers)[]) {
      status.controllers[key] = raw.controllers[key] === true
    }
  }
  if (isRecord(raw.versions)) {
    for (const [kind, version] of Object.entries(raw.versions)) {
      if (typeof version === 'string') status.versions[kind] = version
    }
  }
  return status
}

/** Состояние Flux в активном кластере; от него зависит видимость меню (Sidebar) */
const fluxStatus = ref<FluxStatus>(emptyStatus())
const fluxInstalled = computed(() => fluxStatus.value.installed)

/** Номер последней проверки: ответ устаревшего запроса (другой кластер, повторный refresh) отбрасывается */
let generation = 0

/** Перепроверяет кластер; ошибка (нет прав, кластер недоступен) = «не установлен» */
async function refreshFlux(): Promise<void> {
  const mine = ++generation
  let next = emptyStatus()
  try {
    next = normalizeFluxStatus(await callApp('GetFluxStatus'))
  } catch {
    // нет прав на list CRD, кластер недоступен и т. п.: раздел просто не показывается
  }
  if (mine !== generation) return
  fluxStatus.value = next
  // События flux:changed нужны только пока в кластере есть Flux
  if (next.installed) startFluxLive()
  else stopFluxLive()
}

function resetFlux() {
  generation++
  fluxStatus.value = emptyStatus()
  stopFluxLive()
}

/** Есть ли в кластере контроллер Flux: от него зависит показ подгрупп меню и вкладок */
function hasController(name: keyof FluxControllers): boolean {
  return fluxStatus.value.controllers[name] === true
}

export function useFlux() {
  return { fluxInstalled, fluxStatus, refreshFlux, resetFlux, hasController }
}
