// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { FluxStatus } from '../types/flux'

const handlers = new Map<string, () => void>()
const subscribe = vi.fn((name: string, h: () => void) => {
  handlers.set(name, h)
  return () => { handlers.delete(name) }
})
vi.mock('../platform/events', () => ({ onEvent: (name: string, h: () => void) => subscribe(name, h) }))
vi.mock('../resources/wails', () => ({ callApp: vi.fn() }))

import { callApp } from '../resources/wails'
import { fluxLiveTick } from './useFluxLive'
import { normalizeFluxStatus, useFlux } from './useFlux'

const call = vi.mocked(callApp) as unknown as ReturnType<typeof vi.fn>

const status = (p: Partial<FluxStatus> = {}): FluxStatus => ({
  installed: true,
  state: 'ok',
  controllers: { kustomize: true, helm: true, source: true, notification: false, image: false },
  versions: { Kustomization: 'v1', HelmRelease: 'v2' },
  ...p,
})

beforeEach(() => {
  call.mockReset()
  subscribe.mockClear()
  useFlux().resetFlux()
})
afterEach(() => {
  useFlux().resetFlux()
  fluxLiveTick.value = 0
})

describe('refreshFlux', () => {
  it('Flux установлен: статус сохраняется, подписка на события стартует', async () => {
    call.mockResolvedValue(status())
    const { fluxInstalled, fluxStatus, refreshFlux } = useFlux()
    await refreshFlux()
    expect(call).toHaveBeenCalledWith('GetFluxStatus')
    expect(fluxInstalled.value).toBe(true)
    expect(fluxStatus.value.controllers.helm).toBe(true)
    expect(subscribe).toHaveBeenCalledTimes(1)
    handlers.get('flux:changed')!()
    expect(fluxLiveTick.value).toBe(1)
  })

  it('Flux не установлен: меню скрыто, подписки нет', async () => {
    call.mockResolvedValue(status({ installed: false, state: '', controllers: { kustomize: false, helm: false, source: false, notification: false, image: false }, versions: {} }))
    const { fluxInstalled, refreshFlux } = useFlux()
    await refreshFlux()
    expect(fluxInstalled.value).toBe(false)
    expect(subscribe).not.toHaveBeenCalled()
  })

  it('ошибка (нет прав, кластер недоступен) = «не установлен», без исключения', async () => {
    call.mockRejectedValue(new Error('forbidden'))
    const { fluxInstalled, refreshFlux } = useFlux()
    await expect(refreshFlux()).resolves.toBeUndefined()
    expect(fluxInstalled.value).toBe(false)
  })

  it('ошибка после успеха сбрасывает статус и отписывается', async () => {
    const { fluxInstalled, refreshFlux } = useFlux()
    call.mockResolvedValueOnce(status())
    await refreshFlux()
    expect(handlers.size).toBe(1)
    call.mockRejectedValueOnce(new Error('boom'))
    await refreshFlux()
    expect(fluxInstalled.value).toBe(false)
    expect(handlers.size).toBe(0)
  })

  it('повторный refresh не плодит подписки', async () => {
    call.mockResolvedValue(status())
    const { refreshFlux } = useFlux()
    await refreshFlux()
    await refreshFlux()
    await refreshFlux()
    expect(subscribe).toHaveBeenCalledTimes(1)
    expect(handlers.size).toBe(1)
  })

  it('смена кластера: reset очищает состояние и отписывается', async () => {
    call.mockResolvedValue(status())
    const { fluxInstalled, fluxStatus, refreshFlux, resetFlux } = useFlux()
    await refreshFlux()
    resetFlux()
    expect(fluxInstalled.value).toBe(false)
    expect(fluxStatus.value.controllers.kustomize).toBe(false)
    expect(handlers.size).toBe(0)
  })

  it('ответ прошлого кластера, пришедший после reset, игнорируется', async () => {
    let resolve!: (s: FluxStatus) => void
    call.mockReturnValueOnce(new Promise<FluxStatus>(r => { resolve = r }))
    const { fluxInstalled, refreshFlux, resetFlux } = useFlux()
    const pending = refreshFlux()
    resetFlux()
    resolve(status())
    await pending
    expect(fluxInstalled.value).toBe(false)
    expect(handlers.size).toBe(0)
  })

  it('из двух параллельных refresh побеждает последний', async () => {
    let first!: (s: FluxStatus) => void
    call.mockReturnValueOnce(new Promise<FluxStatus>(r => { first = r }))
    call.mockResolvedValueOnce(status({ installed: false, state: '' }))
    const { fluxInstalled, refreshFlux } = useFlux()
    const a = refreshFlux()
    await refreshFlux()
    first(status())
    await a
    expect(fluxInstalled.value).toBe(false)
  })

  it('unsupportedVersion сохраняется как есть', async () => {
    call.mockResolvedValue(status({ state: 'unsupportedVersion' }))
    const { fluxStatus, refreshFlux } = useFlux()
    await refreshFlux()
    expect(fluxStatus.value.state).toBe('unsupportedVersion')
  })
})

describe('hasController', () => {
  it('отражает присутствие контроллеров; без Flux — всегда false', async () => {
    const { hasController, refreshFlux } = useFlux()
    expect(hasController('helm')).toBe(false)
    call.mockResolvedValue(status())
    await refreshFlux()
    expect(hasController('kustomize')).toBe(true)
    expect(hasController('source')).toBe(true)
    expect(hasController('notification')).toBe(false)
    expect(hasController('image')).toBe(false)
  })
})

describe('normalizeFluxStatus', () => {
  it('мусор вместо статуса даёт «не установлен», без исключений', () => {
    for (const raw of [null, undefined, 42, 'x', [], {}]) {
      const s = normalizeFluxStatus(raw)
      expect(s.installed).toBe(false)
      expect(s.state).toBe('')
      expect(s.controllers).toEqual({ kustomize: false, helm: false, source: false, notification: false, image: false })
      expect(s.versions).toEqual({})
    }
  })

  it('неполный ответ дополняется нулевыми значениями, лишние и не-строковые версии отбрасываются', () => {
    const s = normalizeFluxStatus({
      installed: true,
      state: 'weird',
      controllers: { helm: true, kustomize: 'yes' },
      versions: { Kustomization: 'v1', Bad: 5 },
    })
    expect(s.installed).toBe(true)
    expect(s.state).toBe('')
    expect(s.controllers.helm).toBe(true)
    expect(s.controllers.kustomize).toBe(false)
    expect(s.versions).toEqual({ Kustomization: 'v1' })
  })

  it('installed только при строгом true', () => {
    expect(normalizeFluxStatus({ installed: 'true' }).installed).toBe(false)
    expect(normalizeFluxStatus({ installed: 1 }).installed).toBe(false)
  })
})
