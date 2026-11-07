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
  callApp: vi.fn(async () => []),
}))

import { callApp } from '../../wails'
import { reconcileFlux, requestFluxDelete, setFluxSuspended } from '../../fluxActions'
import { Perm } from '../../../types/auth'
import type { FluxKustomizationInfo } from '../../../types/flux'
import { fluxKustomization as def } from './kustomization'

const SHA = '0123456789abcdef0123456789abcdef01234567'

const ks = (p: Partial<FluxKustomizationInfo> = {}): FluxKustomizationInfo => ({
  kind: 'Kustomization', name: 'apps', namespace: 'flux-system', age: '3d', interval: '10m', suspended: false,
  state: 'Ready', sourceRef: { kind: 'GitRepository', namespace: '', name: 'flux-system' }, dependsOn: [], conditions: [],
  revision: `main@sha1:${SHA}`, lastAttemptedRevision: `main@sha1:${SHA}`, lastReconcileAt: '2026-11-07T10:00:00Z', message: 'Applied revision',
  labels: {}, annotations: {}, created: '2026-11-04T10:00:00Z',
  path: './clusters/prod', prune: true, wait: false, targetNamespace: '', serviceAccount: '', remote: false, inventoryCount: 12, ...p,
})

const col = (key: string) => def.columns.find(c => c.key === key)!
const act = (id: string) => def.actions!.find(a => a.id === id)!
const ctx = { edit: vi.fn(async () => {}) } as any

beforeEach(() => { vi.clearAllMocks() })

describe('определение flux-kustomization', () => {
  it('идентификаторы с префиксом flux-, группа меню flux, namespaced', () => {
    expect(def.id).toBe('flux-kustomization')
    expect(def.view.startsWith('flux-')).toBe(true)
    expect(def.nav?.group).toBe('flux')
    expect(def.namespaced).toBe(true)
  })

  it('ключи колонок уникальны; нужные колонки на месте', () => {
    const keys = def.columns.map(c => c.key)
    expect(new Set(keys).size).toBe(keys.length)
    for (const k of ['name', 'namespace', 'state', 'message', 'revision', 'sourceRef', 'path', 'interval', 'suspended', 'age']) {
      expect(keys).toContain(k)
    }
  })

  it('автообновление по flux:changed подключено', () => {
    expect(def.autoRefresh).toBe(true)
    expect(typeof def.refreshOn).toBe('function')
  })

  it('массовое удаление выключено: удаление по одному с подтверждением', () => {
    expect(def.bulkDelete).toBe(false)
    expect(def.api.remove).toBeUndefined()
  })
})

describe('колонки на данных DTO', () => {
  it('ревизия сокращается, источник и путь показываются', () => {
    const k = ks()
    expect(col('revision').formatter!(k)).toBe('main@0123456')
    expect(col('sourceRef').formatter!(k)).toBe('GitRepository/flux-system')
    expect(col('path').formatter!(k)).toBe('./clusters/prod')
    expect(col('interval').formatter!(k)).toBe('10m')
    expect(col('message').formatter!(k)).toBe('Applied revision')
  })

  it('пустые поля дают прочерк, а неполный DTO не роняет колонки', () => {
    const empty = {} as FluxKustomizationInfo
    for (const key of ['message', 'revision', 'sourceRef', 'path', 'interval']) {
      expect(col(key).formatter!(empty)).toBe('—')
    }
    expect(() => col('state').formatter!(empty)).not.toThrow()
    expect(() => col('suspended').formatter!(empty)).not.toThrow()
  })

  it('suspended: да / нет по значению', () => {
    expect(col('suspended').formatter!(ks({ suspended: true }))).not.toBe(col('suspended').formatter!(ks({ suspended: false })))
  })

  it('цвет состояния: Ready зелёный, Reconciling и Suspended жёлтые, Failed и Stalled красные, Unknown серый', () => {
    const color = (state: FluxKustomizationInfo['state']) => col('state').colorFn!(ks({ state }))
    expect(color('Ready')).toBe('#4ade80')
    expect(color('Reconciling')).toBe('#facc15')
    expect(color('Suspended')).toBe('#facc15')
    expect(color('Failed')).toBe('#ef4444')
    expect(color('Stalled')).toBe('#ef4444')
    expect(color('Unknown')).toBe('#868d8a')
  })

  it('сортировка по состоянию: проблемные выше здоровых', () => {
    const rank = (state: FluxKustomizationInfo['state']) => col('state').sortValue!(ks({ state })) as number
    expect(rank('Failed')).toBeGreaterThan(rank('Ready'))
    expect(rank('Stalled')).toBeGreaterThan(rank('Reconciling'))
  })

  it('подпись состояния для значения вне контракта — как у Unknown', () => {
    const f = col('state').formatter!
    expect(f(ks({ state: 'Weird' as any }))).toBe(f(ks({ state: 'Unknown' })))
  })
})

describe('свойства', () => {
  const prop = (label: string) => def.properties!.find(p => p.label === label)!

  it('lastAttempted показывается только если отличается от применённой ревизии', () => {
    const p = prop('fluxKustomization.props.lastAttempted')
    expect(p.value(ks())).toBeNull()
    expect(p.value(ks({ lastAttemptedRevision: 'main@sha1:' + 'f'.repeat(40) }))).toContain('main@sha1:')
  })

  it('remote и suspended видны только когда true', () => {
    expect(prop('fluxKustomization.props.remote').value(ks())).toBeNull()
    expect(prop('fluxKustomization.props.remote').value(ks({ remote: true }))).not.toBeNull()
    expect(prop('fluxKustomization.columns.suspended').value(ks())).toBeNull()
  })

  it('время reconcile: некорректное значение не даёт Invalid Date', () => {
    const p = prop('fluxKustomization.props.lastReconcile')
    expect(p.value(ks({ lastReconcileAt: 'вчера' }))).toBeNull()
    expect(p.value(ks({ lastReconcileAt: '' }))).toBeNull()
    expect(p.value(ks())).toBeTruthy()
  })
})

describe('действия', () => {
  it('права: reconcile — Operate; suspend, resume, YAML и delete — Write', () => {
    expect(act('reconcile').permission).toBe(Perm.Operate)
    expect(act('reconcileWithSource').permission).toBe(Perm.Operate)
    for (const id of ['suspend', 'resume', 'editYaml', 'delete']) expect(act(id).permission).toBe(Perm.Write)
  })

  it('suspend виден у активного объекта, resume — у приостановленного', () => {
    expect(act('suspend').when!(ks({ suspended: false }))).toBe(true)
    expect(act('suspend').when!(ks({ suspended: true }))).toBe(false)
    expect(act('resume').when!(ks({ suspended: true }))).toBe(true)
    expect(act('resume').when!(ks({ suspended: false }))).toBe(false)
  })

  it('reconcile без источника и с источником', async () => {
    const k = ks()
    await act('reconcile').run(k, ctx)
    await act('reconcileWithSource').run(k, ctx)
    expect(reconcileFlux).toHaveBeenNthCalledWith(1, [k], { withSource: false })
    expect(reconcileFlux).toHaveBeenNthCalledWith(2, [k], { withSource: true })
  })

  it('suspend и resume передают нужное значение', async () => {
    const k = ks()
    await act('suspend').run(k, ctx)
    await act('resume').run(k, ctx)
    expect(setFluxSuspended).toHaveBeenNthCalledWith(1, [k], true)
    expect(setFluxSuspended).toHaveBeenNthCalledWith(2, [k], false)
  })

  it('delete открывает диалог подтверждения, а не удаляет сразу', async () => {
    const k = ks()
    await act('delete').run(k, ctx)
    expect(requestFluxDelete).toHaveBeenCalledWith(k)
    expect(callApp).not.toHaveBeenCalled()
  })

  it('YAML открывается через cr:-вид Kustomization', async () => {
    await act('editYaml').run(ks(), ctx)
    expect(ctx.edit).toHaveBeenCalledWith('cr:kustomize.toolkit.fluxcd.io/v1/kustomizations', 'flux-system', 'apps')
  })

  it('действий Argo и общего удаления нет: опасные операции только через диалоги', () => {
    expect(def.actions!.map(a => a.id)).toEqual(['reconcile', 'reconcileWithSource', 'suspend', 'resume', 'editYaml', 'delete'])
  })
})

describe('данные списка', () => {
  it('list запрашивает GetFluxKustomizations по набору namespace', async () => {
    await def.api.list(['a', 'b'])
    expect(callApp).toHaveBeenCalledWith('GetFluxKustomizations', ['a', 'b'])
  })
})

describe('вкладки', () => {
  it('условия, ресурсы, источник, граф; идентификаторы уникальны', () => {
    const ids = def.tabs!.map(t => t.id)
    expect(ids).toEqual(['fluxConditions', 'fluxResources', 'fluxSource', 'fluxGraph'])
    expect(new Set(ids).size).toBe(ids.length)
  })
})
