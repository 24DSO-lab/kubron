// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from 'vitest'

const handlers = new Map<string, (...a: unknown[]) => void>()
const subscribe = vi.fn((name: string, h: (...a: unknown[]) => void) => {
  handlers.set(name, h)
  return () => { handlers.delete(name) }
})
vi.mock('../platform/events', () => ({ onEvent: (name: string, h: (...a: unknown[]) => void) => subscribe(name, h) }))

import { fluxLiveTick, startFluxLive, stopFluxLive } from './useFluxLive'

afterEach(() => {
  stopFluxLive()
  subscribe.mockClear()
  fluxLiveTick.value = 0
})

describe('useFluxLive', () => {
  it('подписывается на flux:changed, событие увеличивает счётчик', () => {
    startFluxLive()
    expect(subscribe).toHaveBeenCalledTimes(1)
    expect(subscribe.mock.calls[0][0]).toBe('flux:changed')
    handlers.get('flux:changed')!()
    handlers.get('flux:changed')!({ any: 'payload' })
    expect(fluxLiveTick.value).toBe(2)
  })

  it('повторный start не плодит подписки', () => {
    startFluxLive()
    startFluxLive()
    startFluxLive()
    expect(subscribe).toHaveBeenCalledTimes(1)
    expect(handlers.size).toBe(1)
  })

  it('stop отписывается и идемпотентен', () => {
    startFluxLive()
    stopFluxLive()
    expect(handlers.size).toBe(0)
    expect(() => stopFluxLive()).not.toThrow()
  })

  it('после stop события не считаются, повторный start подписывает заново', () => {
    startFluxLive()
    stopFluxLive()
    expect(handlers.get('flux:changed')).toBeUndefined()
    startFluxLive()
    expect(subscribe).toHaveBeenCalledTimes(2)
    handlers.get('flux:changed')!()
    expect(fluxLiveTick.value).toBe(1)
  })
})
