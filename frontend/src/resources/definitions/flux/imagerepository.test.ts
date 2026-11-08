// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../fluxActions', () => ({
  reconcileFlux: vi.fn(async () => {}),
  setFluxSuspended: vi.fn(async () => {}),
  requestFluxDelete: vi.fn(),
  fluxActionTick: { value: 0 },
}))
vi.mock('../../wails', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../wails')>()),
  callApp: vi.fn(),
}))

import { callApp } from '../../wails'
import { reconcileFlux, setFluxSuspended } from '../../fluxActions'
import { Perm } from '../../../types/auth'
import type { FluxImageAutomation, FluxImagePolicyInfo, FluxImageRepositoryInfo, FluxImageUpdateAutomationInfo } from '../../../types/flux'
import { fluxImageRepository } from './imagerepository'
import { fluxImagePolicy } from './imagepolicy'
import { fluxImageUpdateAutomation } from './imageupdateautomation'

const base = {
  namespace: 'flux-system', age: '1d', interval: '5m', suspended: false, state: 'Ready' as const, sourceRef: null, dependsOn: [],
  conditions: [], revision: '', lastAttemptedRevision: '', lastReconcileAt: '', message: '', labels: {}, annotations: {}, created: '',
}
const repo: FluxImageRepositoryInfo = { ...base, kind: 'ImageRepository', name: 'podinfo', image: 'ghcr.io/stefanprodan/podinfo', tagCount: 42, lastScanAt: '2026-11-07T10:00:00Z' }
const policy: FluxImagePolicyInfo = { ...base, kind: 'ImagePolicy', name: 'podinfo', imageRepository: 'podinfo', policy: 'semver: >=6.0.0', latestImage: 'ghcr.io/stefanprodan/podinfo:6.5.0' }
const auto: FluxImageUpdateAutomationInfo = {
  ...base, kind: 'ImageUpdateAutomation', name: 'auto', sourceRef: { kind: 'GitRepository', namespace: '', name: 'flux-system' },
  branch: 'main', lastRunAt: '2026-11-07T11:00:00Z', lastPushCommit: '0123456789abcdef0123456789abcdef01234567',
}
const data: FluxImageAutomation = { repositories: [repo], policies: [policy], automations: [auto] }

const ALL = [
  [fluxImageRepository, 'flux-imagerepository', 'flux-imagerepositories', 'ImageRepository'],
  [fluxImagePolicy, 'flux-imagepolicy', 'flux-imagepolicies', 'ImagePolicy'],
  [fluxImageUpdateAutomation, 'flux-imageupdateautomation', 'flux-imageupdateautomations', 'ImageUpdateAutomation'],
] as const
const ctx = { edit: vi.fn(async () => {}) } as any
const col = (d: { columns: { key: string; formatter?: (row: any) => unknown }[] }, key: string) => d.columns.find(c => c.key === key)!

beforeEach(() => { vi.clearAllMocks(); vi.mocked(callApp as any).mockResolvedValue(data) })

describe('регистрация', () => {
  it('id, view, вид, группа меню; уникальны', () => {
    for (const [d, id, view, kind] of ALL) {
      expect(d.id).toBe(id)
      expect(d.view).toBe(view)
      expect(d.kind).toBe(kind)
      expect(d.nav?.group).toBe('flux-images')
      expect(d.bulkDelete).toBe(false)
      expect(d.api.remove).toBeUndefined()
    }
    expect(new Set(ALL.map(a => a[0].id)).size).toBe(3)
    expect(new Set(ALL.map(a => a[0].view)).size).toBe(3)
  })
})

describe('действия', () => {
  it('ImagePolicy: действий нет', () => {
    expect(fluxImagePolicy.actions ?? []).toEqual([])
  })
  it('ImageRepository и ImageUpdateAutomation: reconcile (Operate), suspend, resume, YAML (Write); delete нет', () => {
    for (const d of [fluxImageRepository, fluxImageUpdateAutomation]) {
      expect(d.actions!.map(a => a.id)).toEqual(['reconcile', 'suspend', 'resume', 'editYaml'])
      expect(d.actions![0].permission).toBe(Perm.Operate)
      for (const a of d.actions!.slice(1)) expect(a.permission).toBe(Perm.Write)
    }
  })
  it('вызовы общих функций и видимость suspend/resume', async () => {
    const a = (id: string) => fluxImageRepository.actions!.find(x => x.id === id)!
    await a('reconcile').run(repo, ctx)
    await a('suspend').run(repo, ctx)
    await a('resume').run(repo, ctx)
    expect(reconcileFlux).toHaveBeenCalledWith([repo], { withSource: false })
    expect(setFluxSuspended).toHaveBeenNthCalledWith(1, [repo], true)
    expect(setFluxSuspended).toHaveBeenNthCalledWith(2, [repo], false)
    expect(a('suspend').when!(repo)).toBe(true)
    expect(a('resume').when!({ ...repo, suspended: true })).toBe(true)
  })
  it('YAML через cr:-вид своего plural', async () => {
    await fluxImageUpdateAutomation.actions!.find(x => x.id === 'editYaml')!.run(auto, ctx)
    expect(ctx.edit).toHaveBeenCalledWith('cr:image.toolkit.fluxcd.io/v1/imageupdateautomations', 'flux-system', 'auto')
  })
})

describe('данные и колонки', () => {
  it('каждый вид берёт свою часть GetFluxImageAutomation', async () => {
    expect((await fluxImageRepository.api.list(['a'])).map(x => x.kind)).toEqual(['ImageRepository'])
    expect((await fluxImagePolicy.api.list(['a'])).map(x => x.kind)).toEqual(['ImagePolicy'])
    expect((await fluxImageUpdateAutomation.api.list(['a'])).map(x => x.kind)).toEqual(['ImageUpdateAutomation'])
    expect(callApp).toHaveBeenCalledWith('GetFluxImageAutomation', ['a'])
  })
  it('пустые и отсутствующие списки — пустой массив', async () => {
    vi.mocked(callApp as any).mockResolvedValue({ repositories: null, policies: undefined, automations: [] })
    for (const [d] of ALL) expect(await d.api.list([])).toEqual([])
  })
  it('ImageRepository: образ, число тегов, последний скан', () => {
    expect(col(fluxImageRepository, 'image').formatter!(repo)).toBe('ghcr.io/stefanprodan/podinfo')
    expect(col(fluxImageRepository, 'tagCount').formatter!(repo)).toBe('42')
    expect(col(fluxImageRepository, 'lastScanAt').formatter!(repo)).not.toBe('—')
    expect(col(fluxImageRepository, 'lastScanAt').formatter!({ ...repo, lastScanAt: '' })).toBe('—')
  })
  it('ImagePolicy: правило и последний образ', () => {
    expect(col(fluxImagePolicy, 'policy').formatter!(policy)).toBe('semver: >=6.0.0')
    expect(col(fluxImagePolicy, 'latestImage').formatter!(policy)).toBe('ghcr.io/stefanprodan/podinfo:6.5.0')
  })
  it('ImageUpdateAutomation: источник, ветка, запуск, короткий коммит', () => {
    expect(col(fluxImageUpdateAutomation, 'sourceRef').formatter!(auto)).toBe('GitRepository/flux-system')
    expect(col(fluxImageUpdateAutomation, 'branch').formatter!(auto)).toBe('main')
    expect(col(fluxImageUpdateAutomation, 'lastPushCommit').formatter!(auto)).toBe('0123456')
  })
  it('пустой DTO не роняет колонки', () => {
    for (const [d] of ALL) for (const c of d.columns) if (c.formatter) expect(() => c.formatter!({} as any)).not.toThrow()
  })
})
