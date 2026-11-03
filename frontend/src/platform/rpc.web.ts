// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Веб-транспорт вызовов бэкенда: POST /api/rpc/{method}, тело — JSON-массив аргументов
 * (internal/server/http.go). Сессия — HttpOnly-cookie, защита от CSRF — заголовок X-CSRF-Token.
 */
import { closeEvents, connectEvents, flushSubscriptions } from './events.web'
import { t } from '../i18n'
import { BUILD_HEADER, BUILD_ID, reloadForNewBuild } from './build'

const CSRF_HEADER = 'X-CSRF-Token'

/** Методы, доступные без сессии (internal/server publicMethods): CSRF-токен им не нужен. */
const PUBLIC_METHODS = new Set(['AuthStatus', 'SetupAdmin', 'Login', 'GetCapabilities'])
/** Методы, для которых 401 — штатный ответ до входа, а не потеря сессии. */
const PRE_LOGIN_METHODS = new Set(['AuthStatus', 'SetupAdmin', 'Login', 'Logout', 'CurrentUser'])

/** Ошибка вызова с машинным кодом сервера (unauthenticated, forbidden, method_denied, …). */
export class RpcError extends Error {
  constructor(message: string, readonly code: string, readonly status: number) {
    super(message)
    this.name = 'RpcError'
  }
}

let csrf = ''

/** Забывает сессию на стороне страницы (выход, 401) и закрывает WebSocket. */
export function resetWebSession() {
  csrf = ''
  closeEvents()
}

async function loadCsrf(): Promise<void> {
  try {
    const r = await fetch('/api/session', { credentials: 'same-origin', headers: { Accept: 'application/json' } })
    const j = await r.json()
    csrf = j?.authenticated ? String(j.csrfToken ?? '') : ''
  } catch {
    csrf = ''
  }
}

interface Reply { status: number; body: any; csrf: string | null }

async function post(method: string, args: unknown[]): Promise<Reply> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json', [BUILD_HEADER]: BUILD_ID }
  if (csrf) headers[CSRF_HEADER] = csrf
  let res: Response
  try {
    res = await fetch(`/api/rpc/${encodeURIComponent(method)}`, {
      method: 'POST', credentials: 'same-origin', headers, body: JSON.stringify(args),
    })
  } catch {
    throw new RpcError(t('platform.noConnection'), 'network', 0)
  }
  let body: any = null
  try { body = await res.json() } catch { /* не JSON: прокси, 502 */ }
  return { status: res.status, body, csrf: res.headers.get(CSRF_HEADER) }
}

export async function rpc(method: string, args: unknown[]): Promise<unknown> {
  await flushSubscriptions()
  if (!csrf && !PUBLIC_METHODS.has(method)) await loadCsrf()

  let reply = await post(method, args)
  if (reply.status === 403 && reply.body?.error?.code === 'csrf') {
    await loadCsrf() // страница могла потерять токен (перезагрузка, вход в другой вкладке)
    reply = await post(method, args)
  }

  if (reply.status >= 200 && reply.status < 300 && !reply.body?.error) {
    if ((method === 'Login' || method === 'SetupAdmin') && reply.csrf) {
      csrf = reply.csrf
      connectEvents()
    } else if (method === 'Logout') {
      resetWebSession()
    }
    return reply.body?.result ?? null
  }

  const err = reply.body?.error
  if (err?.code === 'contract_mismatch') {
    // Страница и сервер разных версий: свежая страница подтянет совместимый набор методов
    reloadForNewBuild()
    throw new RpcError(t('platform.contractMismatch'), 'contract_mismatch', reply.status)
  }
  if (err?.code === 'unauthenticated' && !PRE_LOGIN_METHODS.has(method)) resetWebSession()
  throw new RpcError(
    err?.message || t('platform.httpStatus', { status: reply.status }),
    err?.code || 'error',
    reply.status,
  )
}
