// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { RpcError, resetWebSession, rpc, setSetupToken } from './rpc.web'
import * as events from './events.web'

vi.mock('./events.web', () => ({
  flushSubscriptions: vi.fn(async () => {}),
  connectEvents: vi.fn(),
  closeEvents: vi.fn(),
}))

type Call = { url: string; init?: RequestInit }
let calls: Call[]
let queue: Array<{ status?: number; body?: any; headers?: Record<string, string>; throws?: boolean }>

function reply(): Response {
  const r = queue.shift() ?? { status: 200, body: { result: null } }
  if (r.throws) throw new TypeError('Failed to fetch')
  return new Response(r.body === undefined ? 'not json' : JSON.stringify(r.body), {
    status: r.status ?? 200, headers: r.headers,
  })
}

beforeEach(() => {
  calls = []
  queue = []
  vi.mocked(events.connectEvents).mockClear()
  vi.mocked(events.closeEvents).mockClear()
  globalThis.fetch = vi.fn(async (url: any, init?: any) => { calls.push({ url: String(url), init }); return reply() }) as any
  resetWebSession()
  vi.mocked(events.closeEvents).mockClear()
})

const hdr = (c: Call, name: string) => (c.init?.headers as Record<string, string>)?.[name]

describe('rpc.web', () => {
  it('POST с JSON-массивом аргументов и same-origin cookie, возвращает result', async () => {
    queue.push({ body: { result: 5 } })
    expect(await rpc('GetCapabilities', [1, 'a'])).toBe(5)
    expect(calls[0].url).toBe('/api/rpc/GetCapabilities')
    expect(calls[0].init?.method).toBe('POST')
    expect(calls[0].init?.credentials).toBe('same-origin')
    expect(calls[0].init?.body).toBe('[1,"a"]')
    expect(hdr(calls[0], 'Content-Type')).toBe('application/json')
  })

  it('Login сохраняет CSRF из заголовка и открывает WebSocket; дальше токен отправляется', async () => {
    queue.push({ body: { result: { id: 'u' } }, headers: { 'X-CSRF-Token': 'tok1' } })
    await rpc('Login', ['a', 'b'])
    expect(events.connectEvents).toHaveBeenCalled()
    queue.push({ body: { result: [] } })
    await rpc('GetClusterStatuses', [])
    expect(calls).toHaveLength(2) // /api/session не запрашивался
    expect(hdr(calls[1], 'X-CSRF-Token')).toBe('tok1')
  })

  it('без токена перед защищённым вызовом берёт его из /api/session', async () => {
    queue.push({ body: { authenticated: true, csrfToken: 'fromSession' } })
    queue.push({ body: { result: 1 } })
    await rpc('ListUsers', [])
    expect(calls[0].url).toBe('/api/session')
    expect(hdr(calls[1], 'X-CSRF-Token')).toBe('fromSession')
  })

  it('403 csrf: обновляет токен и повторяет вызов один раз', async () => {
    queue.push({ body: { authenticated: true, csrfToken: 'old' } })
    queue.push({ status: 403, body: { error: { code: 'csrf', message: 'x' } } })
    queue.push({ body: { authenticated: true, csrfToken: 'new' } })
    queue.push({ body: { result: 'ok' } })
    expect(await rpc('ListUsers', [])).toBe('ok')
    expect(hdr(calls[3], 'X-CSRF-Token')).toBe('new')
  })

  it('ошибка сервера → RpcError с кодом и текстом; 401 вне входа сбрасывает сессию', async () => {
    queue.push({ body: { authenticated: true, csrfToken: 't' } })
    queue.push({ status: 401, body: { error: { code: 'unauthenticated', message: 'authentication required' } } })
    const err = (await rpc('ListUsers', []).catch(e => e)) as RpcError
    expect(err).toBeInstanceOf(RpcError)
    expect(err.message).toBe('authentication required')
    expect(err.code).toBe('unauthenticated')
    expect(events.closeEvents).toHaveBeenCalled()
  })

  it('401 на AuthStatus (до входа) сессию не сбрасывает', async () => {
    queue.push({ status: 401, body: { error: { code: 'unauthenticated', message: 'm' } } })
    await rpc('AuthStatus', []).catch(() => {})
    expect(events.closeEvents).not.toHaveBeenCalled()
  })

  it('Logout закрывает WebSocket и забывает токен', async () => {
    queue.push({ body: { result: { id: 'u' } }, headers: { 'X-CSRF-Token': 'tok' } })
    await rpc('Login', ['a', 'b'])
    queue.push({ body: { result: null } })
    await rpc('Logout', [])
    expect(events.closeEvents).toHaveBeenCalled()
    queue.push({ body: { authenticated: false } })
    queue.push({ status: 401, body: { error: { code: 'unauthenticated', message: 'm' } } })
    await rpc('ListUsers', []).catch(() => {})
    expect(calls.at(-2)!.url).toBe('/api/session') // токена нет — запрошен заново
  })

  it('сетевая ошибка и не-JSON ответ дают понятные сообщения', async () => {
    queue.push({ throws: true })
    await expect(rpc('GetCapabilities', [])).rejects.toMatchObject({ code: 'network' })
    queue.push({ status: 502, body: undefined })
    await expect(rpc('GetCapabilities', [])).rejects.toThrow('HTTP 502')
  })

  it('перед вызовом ждёт подтверждения подписок', async () => {
    queue.push({ body: { result: null } })
    await rpc('GetCapabilities', [])
    expect(events.flushSubscriptions).toHaveBeenCalled()
  })

  it('токен первичной настройки уходит только с SetupAdmin и только один раз', async () => {
    setSetupToken('  tok-0123456789abcdef \n')
    queue.push({ body: { result: { id: 'u' } }, headers: { 'X-CSRF-Token': 'c1' } })
    await rpc('SetupAdmin', ['admin', 'pw'])
    expect(hdr(calls[0], 'X-Kubron-Setup-Token')).toBe('tok-0123456789abcdef')
    queue.push({ body: { result: null } })
    await rpc('GetClusterStatuses', [])
    expect(hdr(calls[1], 'X-Kubron-Setup-Token')).toBeUndefined()
    queue.push({ status: 403, body: { error: { code: 'setup_disabled', message: 'x' } } })
    await rpc('SetupAdmin', ['admin', 'pw']).catch(() => {})
    expect(hdr(calls[2], 'X-Kubron-Setup-Token')).toBeUndefined() // после успешной настройки токен забыт
  })

  it('отказ SetupAdmin сохраняет токен для повторной попытки', async () => {
    setSetupToken('tok-0123456789abcdef')
    queue.push({ status: 403, body: { error: { code: 'setup_token_invalid', message: 'bad' } } })
    await expect(rpc('SetupAdmin', ['admin', 'pw'])).rejects.toMatchObject({ code: 'setup_token_invalid' })
    queue.push({ body: { result: { id: 'u' } }, headers: { 'X-CSRF-Token': 'c1' } })
    await rpc('SetupAdmin', ['admin', 'pw'])
    expect(hdr(calls[1], 'X-Kubron-Setup-Token')).toBe('tok-0123456789abcdef')
  })

  it('Login токен настройки не отправляет', async () => {
    setSetupToken('tok-0123456789abcdef')
    queue.push({ body: { result: { id: 'u' } }, headers: { 'X-CSRF-Token': 'c1' } })
    await rpc('Login', ['a', 'b'])
    expect(hdr(calls[0], 'X-Kubron-Setup-Token')).toBeUndefined()
  })
})
