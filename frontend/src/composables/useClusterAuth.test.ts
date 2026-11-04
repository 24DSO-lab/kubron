// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthCancelledError, createClusterAuth, type ClusterAuthDeps } from './useClusterAuth'
import type { Challenge, ConnectResult } from '../types/credential'

const SECRET = 'S3CR3T-control-string-7f3a'
const future = () => new Date(Date.now() + 600_000).toISOString()
const challenge = (p: Partial<Challenge> = {}): Challenge => ({ id: 'c1', kind: 'credentials', expiresAt: future(), ...p })
const asChallenge = (c: Challenge): ConnectResult => ({ status: 'challenge', challenge: c })
const connected: ConnectResult = { status: 'connected' }

function setup(over: Partial<ClusterAuthDeps['api']> = {}) {
  const handlers: { done?: (e: { clusterId: string; challengeId: string }) => void; req?: (e: { clusterId: string }) => void } = {}
  const api = {
    connectCluster: vi.fn(async () => connected),
    answerChallenge: vi.fn(async () => connected),
    pollChallenge: vi.fn(async () => connected),
    cancelChallenge: vi.fn(async () => {}),
    onAuthCompleted: vi.fn((h) => { handlers.done = h; return () => {} }),
    onAuthRequired: vi.fn((h) => { handlers.req = h; return () => {} }),
    ...over,
  } as unknown as ClusterAuthDeps['api']
  const auth = createClusterAuth({
    api,
    now: () => Date.now(),
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    setInterval: (fn, ms) => setInterval(fn, ms),
    clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
  })
  return { auth, api: api as unknown as Record<string, ReturnType<typeof vi.fn>>, handlers }
}

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

describe('connect', () => {
  it('connected сразу: диалог не открывается', async () => {
    const { auth } = setup()
    await auth.connect('k1')
    expect(auth.challenge.value).toBeNull()
  })

  it('credentials: ответ завершает вход и закрывает диалог', async () => {
    const { auth, api } = setup({ connectCluster: vi.fn(async () => asChallenge(challenge())) })
    const done = auth.connect('k1')
    await vi.advanceTimersByTimeAsync(0)
    expect(auth.challenge.value?.kind).toBe('credentials')
    await auth.submit({ username: 'u', password: 'p' })
    await expect(done).resolves.toBeUndefined()
    expect(api.answerChallenge).toHaveBeenCalledWith('c1', { username: 'u', password: 'p' })
    expect(auth.challenge.value).toBeNull()
  })

  it('плохой ввод: диалог открыт, ошибка у поля, поля не очищаются', async () => {
    const { auth } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'manual-token' }))),
      answerChallenge: vi.fn(async () => { throw new Error('the value could not be used') }),
    })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(0)
    await auth.submit({ token: '' })
    expect(auth.challenge.value?.id).toBe('c1')
    expect(auth.error.value).toBe('the value could not be used')
    expect(auth.attempt.value).toBe(0) // счётчик очистки секретных полей не растёт
    expect(auth.busy.value).toBe(false)
  })

  it('сервис входа недоступен: не «неверный пароль», диалог открыт', async () => {
    const { auth } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge())),
      answerChallenge: vi.fn(async () => { throw new Error('the sign-in service is unavailable') }),
    })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(0)
    await auth.submit({ username: 'u', password: 'p' })
    expect(auth.challenge.value?.id).toBe('c1')
    expect(auth.error.value).toBe('the sign-in service is unavailable')
    expect(auth.error.value).not.toContain('rejected')
    expect(auth.attempt.value).toBe(0)
  })

  it('отклонённый ответ: тот же вызов, ошибка и счётчик повторов', async () => {
    const { auth } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge())),
      answerChallenge: vi.fn(async () => { throw new Error('the credentials were rejected') }),
    })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(0)
    await auth.submit({ username: 'u', password: 'bad' })
    expect(auth.challenge.value?.id).toBe('c1')
    expect(auth.error.value).not.toBe('')
    expect(auth.attempt.value).toBe(1)
    expect(auth.busy.value).toBe(false)
  })

  it('следующий вызов заменяет текущий (например, после выбора метода)', async () => {
    const { auth } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'confirm-exec' }))),
      answerChallenge: vi.fn(async () => asChallenge(challenge({ id: 'c2', kind: 'manual-token' }))),
    })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(0)
    await auth.submit({ decision: 'once' })
    expect(auth.challenge.value?.id).toBe('c2')
  })
})

describe('опрос device-code', () => {
  it('опрашивает по intervalSec и закрывается при connected', async () => {
    const poll = vi.fn()
      .mockResolvedValueOnce(asChallenge(challenge({ kind: 'device-code', poll: true, intervalSec: 3 })))
      .mockResolvedValueOnce(connected)
    const { auth } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'device-code', poll: true, intervalSec: 3, userCode: 'ABCD-1234' }))),
      pollChallenge: poll,
    })
    const done = auth.connect('k1')
    await vi.advanceTimersByTimeAsync(0)
    expect(poll).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(3000)
    expect(poll).toHaveBeenCalledTimes(1)
    expect(auth.challenge.value).not.toBeNull()
    await vi.advanceTimersByTimeAsync(3000)
    await expect(done).resolves.toBeUndefined()
    expect(auth.challenge.value).toBeNull()
  })

  it('обратный отсчёт идёт и доходит до «истёк»', async () => {
    const soon = new Date(Date.now() + 5000).toISOString()
    const { auth } = setup({ connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'device-code', expiresAt: soon }))) })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(0)
    expect(auth.remainingSec.value).toBe(5)
    await vi.advanceTimersByTimeAsync(6000)
    expect(auth.remainingSec.value).toBe(0)
    expect(auth.expired.value).toBe(true)
  })

  it('истёкший вызов на сервере останавливает опрос с ошибкой', async () => {
    const poll = vi.fn(async () => { throw new Error('the sign-in request has expired') })
    const { auth } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'device-code', poll: true, intervalSec: 1 }))),
      pollChallenge: poll,
    })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(5000)
    expect(poll).toHaveBeenCalledTimes(1)
    expect(auth.error.value).not.toBe('')
    expect(auth.challenge.value).not.toBeNull()
  })
})

describe('отмена и события', () => {
  it('cancel: AuthCancelledError, бэкенд уведомлён, опрос остановлен', async () => {
    const { auth, api } = setup({ connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'device-code', poll: true, intervalSec: 1 }))) })
    const done = auth.connect('k1')
    const assertion = expect(done).rejects.toBeInstanceOf(AuthCancelledError)
    await vi.advanceTimersByTimeAsync(0)
    await auth.cancel()
    await assertion
    expect(api.cancelChallenge).toHaveBeenCalledWith('c1')
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.pollChallenge).not.toHaveBeenCalled()
  })

  it('cluster:auth-completed своего кластера закрывает диалог, чужого — нет', async () => {
    const { auth, handlers } = setup({ connectCluster: vi.fn(async () => asChallenge(challenge({ kind: 'browser-loopback' }))) })
    auth.listen()
    const done = auth.connect('k1')
    await vi.advanceTimersByTimeAsync(0)
    handlers.done?.({ clusterId: 'other', challengeId: 'x' })
    expect(auth.challenge.value).not.toBeNull()
    handlers.done?.({ clusterId: 'k1', challengeId: 'c1' })
    await expect(done).resolves.toBeUndefined()
    expect(auth.challenge.value).toBeNull()
  })

  it('cluster:auth-required передаётся вызывающему', () => {
    const { auth, handlers } = setup()
    const spy = vi.fn()
    auth.listen(spy)
    handlers.req?.({ clusterId: 'k9' })
    expect(spy).toHaveBeenCalledWith('k9')
  })
})

describe('секреты не остаются на клиенте', () => {
  it('контрольная строка не попадает в состояние, хранилище браузера, логи и ошибки', async () => {
    const store = new Map<string, string>()
    vi.stubGlobal('localStorage', { setItem: (k: string, v: string) => store.set(k, v), getItem: (k: string) => store.get(k) ?? null })
    vi.stubGlobal('sessionStorage', { setItem: (k: string, v: string) => store.set(k, v), getItem: (k: string) => store.get(k) ?? null })
    const logs: unknown[][] = []
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map((m) => vi.spyOn(console, m).mockImplementation((...a) => { logs.push(a) }))

    const { auth, api } = setup({
      connectCluster: vi.fn(async () => asChallenge(challenge())),
      answerChallenge: vi.fn(async () => { throw new Error('the credentials were rejected') }),
    })
    void auth.connect('k1').catch(() => {})
    await vi.advanceTimersByTimeAsync(0)
    await auth.submit({ username: 'u', password: SECRET })

    // значение дошло до RPC (иначе тест ничего не проверяет) ...
    expect(JSON.stringify(api.answerChallenge.mock.calls)).toContain(SECRET)
    // ... но больше нигде не осталось
    const state = JSON.stringify({
      challenge: auth.challenge.value, clusterId: auth.clusterId.value, busy: auth.busy.value,
      error: auth.error.value, attempt: auth.attempt.value,
    })
    expect(state).not.toContain(SECRET)
    expect([...store.values()].join('|')).not.toContain(SECRET)
    expect(JSON.stringify(logs)).not.toContain(SECRET)
    spies.forEach((s) => s.mockRestore())
    vi.unstubAllGlobals()
  })
})
