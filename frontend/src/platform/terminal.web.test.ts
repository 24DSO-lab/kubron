// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { openWebTerminal } from './terminal.web'
import { callApp } from './api'

vi.mock('./api', () => ({ callApp: vi.fn() }))

class FakeWS {
  static last: FakeWS
  binaryType = ''
  readyState = 0
  sent: unknown[] = []
  onopen: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onclose: ((e: { code: number; reason: string }) => void) | null = null
  onerror: (() => void) | null = null
  closedWith: number | undefined
  constructor(public url: string) { FakeWS.last = this }
  open() { this.readyState = 1; this.onopen?.() }
  send(d: unknown) { this.sent.push(d) }
  close(code?: number) { this.closedWith = code; this.readyState = 3 }
  serverClose(code: number, reason: string) { this.readyState = 3; this.onclose?.({ code, reason }) }
}

const params = { namespace: 'prod', pod: 'web-1', container: 'app', shell: '/bin/sh', cols: 100, rows: 30 }

beforeEach(() => {
  vi.mocked(callApp).mockReset()
  ;(globalThis as any).WebSocket = FakeWS
  ;(globalThis as any).location = { protocol: 'https:', host: 'kubron.test' }
})

async function open(handlers = { onData: vi.fn(), onExit: vi.fn() }) {
  vi.mocked(callApp).mockResolvedValueOnce('abc123' as never)
  const p = openWebTerminal(params, handlers)
  await vi.waitFor(() => expect(FakeWS.last).toBeDefined())
  await Promise.resolve()
  FakeWS.last.open()
  return { handle: await p, ws: FakeWS.last, handlers }
}

describe('openWebTerminal', () => {
  it('OpenTerminal выдаёт id, к нему подключается бинарный WebSocket', async () => {
    const { ws } = await open()
    expect(callApp).toHaveBeenCalledWith('OpenTerminal', 'prod', 'web-1', 'app', '/bin/sh', 100, 30)
    expect(ws.url).toBe('wss://kubron.test/api/terminal/abc123')
    expect(ws.binaryType).toBe('arraybuffer')
  })

  it('вывод — бинарные кадры → onData', async () => {
    const { ws, handlers } = await open()
    ws.onmessage?.({ data: new Uint8Array([104, 105]).buffer })
    expect(handlers.onData).toHaveBeenCalledWith(new Uint8Array([104, 105]))
    ws.onmessage?.({ data: 'текст игнорируется' })
    expect(handlers.onData).toHaveBeenCalledTimes(1)
  })

  it('ввод — бинарный кадр UTF-8, размер — управляющий JSON', async () => {
    const { handle, ws } = await open()
    handle.write('ls é\n')
    handle.resize(120, 40)
    expect(ws.sent[0]).toEqual(new TextEncoder().encode('ls é\n'))
    expect(JSON.parse(ws.sent[1] as string)).toEqual({ type: 'resize', cols: 120, rows: 40 })
  })

  it('закрытие 1000 «exit» → exit, иное → lost', async () => {
    const a = await open()
    a.ws.serverClose(1000, 'exit')
    expect(a.handlers.onExit).toHaveBeenCalledWith('exit')
    const b = await open()
    b.ws.serverClose(1006, '')
    expect(b.handlers.onExit).toHaveBeenCalledWith('lost')
  })

  it('close() закрывает сокет, завершает процесс RPC и не сообщает onExit', async () => {
    vi.mocked(callApp).mockResolvedValue(undefined as never)
    const { handle, ws, handlers } = await open()
    handle.close()
    expect(ws.closedWith).toBe(1000)
    expect(callApp).toHaveBeenCalledWith('CloseTerminal', 'abc123')
    ws.serverClose(1000, '')
    expect(handlers.onExit).not.toHaveBeenCalled()
  })

  it('ошибка подключения сокета после всех попыток → исключение', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(callApp).mockResolvedValueOnce('id' as never)
      const p = openWebTerminal(params, { onData: vi.fn(), onExit: vi.fn() })
      const rejected = expect(p).rejects.toThrow('Could not connect')
      for (let i = 0; i < 8; i++) {
        await vi.advanceTimersByTimeAsync(400)
        FakeWS.last.serverClose(1006, '')
      }
      await rejected
    } finally {
      vi.useRealTimers()
    }
  })

  it('сокет не открылся на реплике без терминала (409) → повтор, затем подключение', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(callApp).mockResolvedValueOnce('r1.abc' as never)
      const p = openWebTerminal(params, { onData: vi.fn(), onExit: vi.fn() })
      await vi.advanceTimersByTimeAsync(0)
      const first = FakeWS.last
      first.serverClose(1006, '') // ответ 409 от другой реплики виден браузеру как сбой подключения
      await vi.advanceTimersByTimeAsync(400)
      expect(FakeWS.last).not.toBe(first)
      expect(FakeWS.last.url).toBe(first.url)
      FakeWS.last.open()
      await expect(p).resolves.toBeDefined()
    } finally {
      vi.useRealTimers()
    }
  })

  it('ошибка OpenTerminal пробрасывается', async () => {
    vi.mocked(callApp).mockRejectedValueOnce(new Error('нет прав'))
    await expect(openWebTerminal(params, { onData: vi.fn(), onExit: vi.fn() })).rejects.toThrow('нет прав')
  })
})
