// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readonly, ref } from 'vue'
import { callApp } from '../platform/api'

/**
 * Возможности оболочки, в которой запущен бэкенд (Go: app.Capabilities, ADR-0010).
 * Интерфейс скрывает недоступное по флагам, а не по проверкам «десктоп или веб».
 * Это удобство интерфейса, а не защита: права проверяет бэкенд.
 */
export interface Capabilities {
  /** нативный диалог выбора файла */
  fileDialog: boolean
  /** проброс портов */
  portForward: boolean
  /** как открывается проброс: local — порт на localhost, proxy — через прокси сервера, off — недоступен */
  portForwardMode: 'local' | 'proxy' | 'off'
  /** оболочка на узле */
  nodeShell: boolean
  /** чтение kubeconfig с диска машины, где запущен бэкенд */
  localKubeconfig: boolean
  /** вход через SSO/OIDC (платный модуль) */
  sso: boolean
  /** сервер работает внутри Kubernetes: можно добавить его собственный кластер (in-cluster) */
  inCluster: boolean
  /** команды и кластеры по командам (сервисный режим) */
  teams: boolean
}

/**
 * Значения до загрузки и при её ошибке: как в десктопе до появления флагов, чтобы поведение
 * не менялось. Веб-сервер перезаписывает их ответом бэкенда.
 */
export const DEFAULT_CAPABILITIES: Readonly<Capabilities> = Object.freeze({
  fileDialog: true,
  portForward: true,
  portForwardMode: 'local',
  nodeShell: true,
  localKubeconfig: true,
  sso: false,
  inCluster: false,
  teams: false,
})

/** Берёт из ответа только известные булевы флаги; остальное — значения по умолчанию. */
export function normalizeCapabilities(raw: unknown): Capabilities {
  const out: Capabilities = { ...DEFAULT_CAPABILITIES }
  if (raw && typeof raw === 'object') {
    for (const key of Object.keys(out) as (keyof Capabilities)[]) {
      const v = (raw as Record<string, unknown>)[key]
      if (key === 'portForwardMode') {
        if (v === 'local' || v === 'proxy' || v === 'off') out.portForwardMode = v
      } else if (typeof v === 'boolean') (out as unknown as Record<string, boolean>)[key] = v
    }
  }
  return out
}

const capabilities = ref<Capabilities>({ ...DEFAULT_CAPABILITIES })
const loaded = ref(false)

/** Загружает возможности с бэкенда (метод публичный: нужен до входа). Не бросает исключений. */
export async function loadCapabilities(): Promise<void> {
  try {
    capabilities.value = normalizeCapabilities(await callApp('GetCapabilities'))
  } catch {
    capabilities.value = { ...DEFAULT_CAPABILITIES }
  } finally {
    loaded.value = true
  }
}

export function useCapabilities() {
  return { capabilities: readonly(capabilities), loaded: readonly(loaded), loadCapabilities }
}
