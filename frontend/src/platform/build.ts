// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Версия сборки страницы для проверки RPC-контракта (internal/server/contract.go). Образ задаёт её переменной
 * VITE_KUBRON_BUILD (= VERSION); без неё остаётся "dev", и сервер проверку не делает.
 */
export const BUILD_ID: string = import.meta.env.VITE_KUBRON_BUILD || 'dev'

export const BUILD_HEADER = 'X-Kubron-Build'

/** Добавляет версию сборки в адрес WebSocket (заголовки при открытии сокета браузер не даёт задать). */
export function withBuild(url: string, build: string = BUILD_ID): string {
  if (!build || build === 'dev') return url
  return `${url}${url.includes('?') ? '&' : '?'}build=${encodeURIComponent(build)}`
}

/** Интервал, чаще которого страница не перезагружается сама: защита от цикла при закэшированном старом index.html. */
export const RELOAD_GUARD_MS = 60_000

/** Можно ли перезагрузить страницу сейчас; last — время прошлой автоперезагрузки (мс) или null. */
export function mayReload(now: number, last: number | null): boolean {
  return last === null || !Number.isFinite(last) || now - last >= RELOAD_GUARD_MS
}

const RELOAD_KEY = 'kubron.contractReload'

/** Перезагружает страницу после contract_mismatch, но не чаще раза в RELOAD_GUARD_MS. true — перезагрузка запущена. */
export function reloadForNewBuild(): boolean {
  let last: number | null = null
  try {
    const v = sessionStorage.getItem(RELOAD_KEY)
    last = v === null ? null : Number(v)
  } catch { /* хранилище недоступно: считаем, что перезагрузки не было */ }
  const now = Date.now()
  if (!mayReload(now, last)) return false
  try { sessionStorage.setItem(RELOAD_KEY, String(now)) } catch { /* без хранилища перезагрузка всё равно допустима */ }
  location.reload()
  return true
}
