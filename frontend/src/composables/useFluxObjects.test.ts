// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../resources/wails', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../resources/wails')>()),
  callApp: vi.fn(),
}))

import { callApp } from '../resources/wails'
import type { FluxHelmReleaseInfo, FluxKustomizationInfo, FluxStatus } from '../types/flux'
import { loadFluxObjects } from './useFluxObjects'

const call = vi.mocked(callApp) as unknown as ReturnType<typeof vi.fn>
const status = (p: Partial<FluxStatus> = {}): FluxStatus => ({
  installed: true, state: 'ok', versions: {},
  controllers: { kustomize: true, helm: true, source: true, notification: false, image: false }, ...p,
})
const ks = [{ kind: 'Kustomization', name: 'apps', namespace: 'flux-system' }] as FluxKustomizationInfo[]
const hr = [{ kind: 'HelmRelease', name: 'web', namespace: 'apps' }] as FluxHelmReleaseInfo[]

beforeEach(() => {
  call.mockReset()
  call.mockImplementation(async (m: string) => (m === 'GetFluxKustomizations' ? ks : hr))
})

describe('loadFluxObjects', () => {
  it('читает оба вида из всех namespace', async () => {
    const r = await loadFluxObjects(status())
    expect(call).toHaveBeenCalledWith('GetFluxKustomizations', [])
    expect(call).toHaveBeenCalledWith('GetFluxHelmReleases', [])
    expect(r.kustomizations).toEqual(ks)
    expect(r.helmReleases).toEqual(hr)
    expect(r.errors).toEqual([])
  })

  it('вид без контроллера не запрашивается', async () => {
    const r = await loadFluxObjects(status({ controllers: { kustomize: true, helm: false, source: true, notification: false, image: false } }))
    expect(call).toHaveBeenCalledTimes(1)
    expect(r.helmReleases).toEqual([])
  })

  it('Flux не установлен или версия не поддерживается: запросов нет, списки пустые', async () => {
    for (const s of [status({ installed: false }), status({ state: 'unsupportedVersion' })]) {
      const r = await loadFluxObjects(s)
      expect(r).toEqual({ kustomizations: [], helmReleases: [], errors: [] })
    }
    expect(call).not.toHaveBeenCalled()
  })

  it('ошибка одного вида не отменяет другой: null означает «оставь прежние данные»', async () => {
    call.mockImplementation(async (m: string) => {
      if (m === 'GetFluxHelmReleases') throw new Error('forbidden')
      return ks
    })
    const r = await loadFluxObjects(status())
    expect(r.kustomizations).toEqual(ks)
    expect(r.helmReleases).toBeNull()
    expect(r.errors).toHaveLength(1)
    expect(r.errors[0]).toContain('forbidden')
  })

  it('обе загрузки упали: оба null, две ошибки, исключения нет', async () => {
    call.mockRejectedValue(new Error('down'))
    const r = await loadFluxObjects(status())
    expect(r.kustomizations).toBeNull()
    expect(r.helmReleases).toBeNull()
    expect(r.errors).toHaveLength(2)
  })

  it('null вместо списка превращается в пустой список', async () => {
    call.mockResolvedValue(null)
    const r = await loadFluxObjects(status())
    expect(r.kustomizations).toEqual([])
    expect(r.helmReleases).toEqual([])
  })

  it('неполный статус без controllers не роняет загрузку', async () => {
    const r = await loadFluxObjects({ installed: true, state: 'ok', versions: {} } as unknown as FluxStatus)
    expect(r.kustomizations).toEqual([])
    expect(call).not.toHaveBeenCalled()
  })
})
