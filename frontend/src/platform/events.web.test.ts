// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CLOSE_SERVICE_RESTART, RECONNECTED, __resetForTests, closeEvents, connectEvents, flushSubscriptions, onEvent } from './events.web'

class FakeWS {
  static all: FakeWS[] = []
  readyState = 1
  sent: any[] = []
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: ((e: { code?: number }) => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) { FakeWS.all.push(this) }
  send(s: string) { this.sent.push(JSON.parse(s)) }
  close() { this.readyState = 3; this.onclose?.({}) }
  // сервер → клиент
  push(o: object) { this.onmessage?.({ data: JSON.stringify(o) }) }
  drop(code?: number) { this.readyState = 3; this.onclose?.({ code }) }
}
const last = () => FakeWS.all[FakeWS.all.length - 1]

beforeEach(() => {
  vi.useFakeTimers()
  FakeWS.all = []
  ;(globalThis as any).WebSocket = FakeWS
  ;(globalThis as any).location = { protocol: 'http:', host: 'kubron.test' }
  __resetForTests()
})
afterEach(() => { __resetForTests(); vi.useRealTimers() })

describe('events.web', () => {
  it('подключается лениться по первой подписке и шлёт sub после ready', () => {
    expect(FakeWS.all).toHaveLength(0)
    const got: any[] = []
    onEvent('cluster:changed', (...a) => got.push(a))
    expect(FakeWS.all).toHaveLength(1)
    expect(last().url).toBe('ws://kubron.test/api/ws')
    last().push({ op: 'ready' })
    expect(last().sent).toEqual([{ op: 'sub', events: ['cluster:changed'] }])
    last().push({ event: 'cluster:changed', data: ['prod', 2] })
    expect(got).toEqual([['prod', 2]])
  })

  it('https → wss', () => {
    ;(globalThis as any).location = { protocol: 'https:', host: 'k.example' }
    onEvent('a', () => {})
    expect(last().url).toBe('wss://k.example/api/ws')
  })

  it('sub/unsub уходят только при первом и последнем обработчике', () => {
    onEvent('x', () => {})
    last().push({ op: 'ready' })
    last().sent.length = 0
    const off1 = onEvent('y', () => {})
    const off2 = onEvent('y', () => {})
    expect(last().sent).toEqual([{ op: 'sub', events: ['y'] }])
    off1()
    expect(last().sent).toHaveLength(1)
    off2()
    expect(last().sent[1]).toEqual({ op: 'unsub', events: ['y'] })
  })

  it('переподключается с паузой, переподписывается и сообщает RECONNECTED', () => {
    const rec = vi.fn()
    onEvent('a', () => {})
    onEvent(RECONNECTED, rec)
    last().push({ op: 'ready' })
    expect(rec).not.toHaveBeenCalled() // первое подключение — не переподключение
    last().drop()
    expect(FakeWS.all).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(FakeWS.all).toHaveLength(2)
    last().push({ op: 'ready' })
    expect(last().sent[0].events.sort()).toEqual(['a', RECONNECTED].sort())
    expect(rec).toHaveBeenCalledTimes(1)
  })

  it('после 1012 переподключается быстро и не наращивает паузу', () => {
    onEvent('a', () => {})
    last().push({ op: 'ready' })
    // серия обрывов без успешного ready: обычная пауза выросла бы до 4 с и больше
    for (let i = 0; i < 3; i++) {
      last().drop()
      vi.advanceTimersByTime(60000)
    }
    const before = FakeWS.all.length
    last().drop(CLOSE_SERVICE_RESTART)
    expect(FakeWS.all).toHaveLength(before)
    vi.advanceTimersByTime(1200)
    expect(FakeWS.all).toHaveLength(before + 1)
    last().push({ op: 'ready' })
    expect(last().sent[0]).toEqual({ op: 'sub', events: ['a'] }) // подписки восстановлены
  })

  it('503 при подключении (реплика в draining) повторяется с обычной паузой', () => {
    onEvent('a', () => {})
    last().push({ op: 'ready' })
    last().drop(CLOSE_SERVICE_RESTART)
    vi.advanceTimersByTime(1200)
    const n = FakeWS.all.length
    last().drop(1006) // отказ в рукопожатии: код 1006
    vi.advanceTimersByTime(100)
    expect(FakeWS.all).toHaveLength(n) // пауза с нарастанием, а не мгновенный повтор
    vi.advanceTimersByTime(2000)
    expect(FakeWS.all).toHaveLength(n + 1)
  })

  it('closeEvents не переподключается до connectEvents', () => {
    onEvent('a', () => {})
    last().push({ op: 'ready' })
    closeEvents()
    vi.advanceTimersByTime(60000)
    expect(FakeWS.all).toHaveLength(1)
    connectEvents()
    expect(FakeWS.all).toHaveLength(2)
  })

  it('исключение в обработчике не ломает остальные', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const ok = vi.fn()
    onEvent('e', () => { throw new Error('boom') })
    onEvent('e', ok)
    last().push({ op: 'ready' })
    last().push({ event: 'e', data: [1] })
    expect(ok).toHaveBeenCalledWith(1)
    spy.mockRestore()
  })

  it('flushSubscriptions ждёт sub_ok, а без подписчиков не ждёт', async () => {
    await flushSubscriptions() // подписчиков нет
    onEvent('t', () => {})
    let done = false
    const p = flushSubscriptions().then(() => { done = true })
    last().push({ op: 'ready' }) // ушёл sub, ждём подтверждения
    await Promise.resolve()
    expect(done).toBe(false)
    last().push({ op: 'sub_ok', events: ['t'] })
    await p
    expect(done).toBe(true)
  })

  it('flushSubscriptions не блокирует вызовы дольше таймаута', async () => {
    onEvent('t', () => {}) // ready так и не придёт
    const p = flushSubscriptions()
    vi.advanceTimersByTime(2100)
    await expect(p).resolves.toBeUndefined()
  })
})
