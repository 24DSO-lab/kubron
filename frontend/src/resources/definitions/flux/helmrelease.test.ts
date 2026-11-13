// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// Registry imports resource definitions that transitively load Monaco Editor.
// Keep these SSR tests in Node without evaluating Monaco's browser-only modules.
vi.mock('monaco-editor', () => ({ editor: {} }))
vi.mock('monaco-editor/editor/editor.api', () => ({ editor: {}, KeyMod: {}, KeyCode: {} }))
vi.mock('monaco-editor/editor/editor.worker?worker', () => ({ default: class {} }))

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

import { i18n } from '../../../i18n'
import { callApp } from '../../wails'
import { reconcileFlux, requestFluxDelete, setFluxSuspended } from '../../fluxActions'
import { Perm } from '../../../types/auth'
import type { FluxHelmReleaseInfo } from '../../../types/flux'
import { fluxHelmRelease as def, helmChart, HelmResourcesTab } from './helmrelease'

const hr = (p: Partial<FluxHelmReleaseInfo> = {}): FluxHelmReleaseInfo => ({
  kind: 'HelmRelease', name: 'podinfo', namespace: 'apps', age: '2d', interval: '10m', suspended: false, state: 'Ready',
  sourceRef: null, dependsOn: [], conditions: [], revision: '6.5.0', lastAttemptedRevision: '6.5.0',
  lastReconcileAt: '2026-11-07T10:00:00Z', message: 'ok', labels: {}, annotations: {}, created: '2026-11-06T10:00:00Z',
  chart: 'podinfo', chartVersion: '6.5.0', chartRef: null, releaseName: 'podinfo', targetNamespace: 'podinfo',
  storageNamespace: '', remote: false, hasInventory: true, inventoryCount: 3, history: [], ...p,
})

const col = (key: string) => def.columns.find(c => c.key === key)!
const act = (id: string) => def.actions!.find(a => a.id === id)!
const ctx = { edit: vi.fn(async () => {}) } as any

beforeEach(() => { vi.clearAllMocks() })

describe('определение flux-helmrelease', () => {
  it('идентификаторы, группа меню, namespaced', () => {
    expect(def.id).toBe('flux-helmrelease')
    expect(def.view).toBe('flux-helmreleases')
    expect(def.nav?.group).toBe('flux')
    expect(def.namespaced).toBe(true)
    expect(def.bulkDelete).toBe(false)
    expect(def.api.remove).toBeUndefined()
  })
  it('ключи колонок уникальны, id вкладок уникальны', () => {
    const keys = def.columns.map(c => c.key)
    expect(new Set(keys).size).toBe(keys.length)
    const ids = def.tabs!.map(t => t.id)
    expect(ids).toEqual(['fluxConditions', 'fluxHistory', 'fluxResources', 'fluxSource', 'fluxGraph'])
  })
})

describe('колонки', () => {
  it('чарт имя@версия; без версии — имя; без spec.chart — из chartRef', () => {
    expect(col('chart').formatter!(hr())).toBe('podinfo@6.5.0')
    expect(helmChart(hr({ chartVersion: '' }))).toBe('podinfo')
    expect(helmChart(hr({ chart: '', chartVersion: '', chartRef: { kind: 'OCIRepository', namespace: 'flux-system', name: 'podinfo' } })))
      .toBe('OCIRepository/flux-system/podinfo')
  })
  it('пустой DTO не роняет колонки и даёт прочерки', () => {
    const empty = {} as FluxHelmReleaseInfo
    for (const k of ['chart', 'releaseName', 'targetNamespace', 'revision']) expect(col(k).formatter!(empty)).toBe('—')
    expect(() => col('state').formatter!(empty)).not.toThrow()
  })
  it('цвета состояний', () => {
    const c = (state: FluxHelmReleaseInfo['state']) => col('state').colorFn!(hr({ state }))
    expect(c('Ready')).toBe('var(--green)')
    expect(c('Reconciling')).toBe('var(--yellow)')
    expect(c('Stalled')).toBe('var(--red)')
    expect(c('Failed')).toBe('var(--red)')
  })
  it('ревизия сокращается', () => {
    expect(col('revision').formatter!(hr({ revision: 'sha256:' + 'a'.repeat(64) }))).toBe('sha256:aaaaaaa')
  })
})

describe('действия', () => {
  it('права и видимость', () => {
    expect(act('reconcile').permission).toBe(Perm.Operate)
    expect(act('reconcileWithSource').permission).toBe(Perm.Operate)
    for (const id of ['suspend', 'resume', 'editYaml', 'delete']) expect(act(id).permission).toBe(Perm.Write)
    expect(act('suspend').when!(hr())).toBe(true)
    expect(act('resume').when!(hr({ suspended: true }))).toBe(true)
  })
  it('вызовы общих функций fluxActions', async () => {
    const r = hr()
    await act('reconcile').run(r, ctx)
    await act('reconcileWithSource').run(r, ctx)
    await act('suspend').run(r, ctx)
    await act('resume').run(r, ctx)
    await act('delete').run(r, ctx)
    expect(reconcileFlux).toHaveBeenNthCalledWith(1, [r], { withSource: false })
    expect(reconcileFlux).toHaveBeenNthCalledWith(2, [r], { withSource: true })
    expect(setFluxSuspended).toHaveBeenNthCalledWith(1, [r], true)
    expect(setFluxSuspended).toHaveBeenNthCalledWith(2, [r], false)
    expect(requestFluxDelete).toHaveBeenCalledWith(r)
    expect(callApp).not.toHaveBeenCalled()
  })
  it('YAML через cr:-вид HelmRelease (v2 по умолчанию)', async () => {
    await act('editYaml').run(hr(), ctx)
    expect(ctx.edit).toHaveBeenCalledWith('cr:helm.toolkit.fluxcd.io/v2/helmreleases', 'apps', 'podinfo')
  })
})

describe('вкладка ресурсов', () => {
  const render = (resource: Partial<FluxHelmReleaseInfo>) =>
    renderToString(createSSRApp({ render: () => h(HelmResourcesTab, { resource }) }).use(i18n))

  it('без inventory (Flux до 2.8) — пояснение, запрос графа не делается', async () => {
    const html = await render(hr({ hasInventory: false }))
    expect(html).toContain('data-testid="flux-helm-noinventory"')
    expect(callApp).not.toHaveBeenCalled()
  })
  it('с inventory — вкладка ресурсов', async () => {
    const html = await render(hr({ hasInventory: true }))
    expect(html).toContain('data-testid="flux-inventory"')
  })
})
