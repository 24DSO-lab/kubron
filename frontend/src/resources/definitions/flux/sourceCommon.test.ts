// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
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

import { i18n } from '../../../i18n'
import { callApp } from '../../wails'
import { reconcileFlux, setFluxSuspended } from '../../fluxActions'
import { Perm } from '../../../types/auth'
import type { FluxSourceInfo } from '../../../types/flux'
import { SourceArtifactTab, sourceRefText } from './sourceCommon'
import { fluxGitRepository } from './source-gitrepository'
import { fluxOCIRepository } from './source-ocirepository'
import { fluxHelmRepository } from './source-helmrepository'
import { fluxHelmChart } from './source-helmchart'
import { fluxBucket } from './source-bucket'

const ALL = [
  [fluxGitRepository, 'GitRepository', 'flux-gitrepository', 'flux-gitrepositories', 'gitrepositories'],
  [fluxOCIRepository, 'OCIRepository', 'flux-ocirepository', 'flux-ocirepositories', 'ocirepositories'],
  [fluxHelmRepository, 'HelmRepository', 'flux-helmrepository', 'flux-helmrepositories', 'helmrepositories'],
  [fluxHelmChart, 'HelmChart', 'flux-helmchart', 'flux-helmcharts', 'helmcharts'],
  [fluxBucket, 'Bucket', 'flux-bucket', 'flux-buckets', 'buckets'],
] as const

const SHA = '0123456789abcdef0123456789abcdef01234567'
const src = (p: Partial<FluxSourceInfo> = {}): FluxSourceInfo => ({
  kind: 'GitRepository', name: 'flux-system', namespace: 'flux-system', age: '5d', interval: '1m', suspended: false, state: 'Ready',
  sourceRef: null, dependsOn: [], conditions: [], revision: `main@sha1:${SHA}`, lastAttemptedRevision: '',
  lastReconcileAt: '2026-11-07T10:00:00Z', message: 'stored artifact', labels: {}, annotations: {}, created: '',
  url: 'https://github.com/org/repo', refType: 'branch', refValue: 'main', digest: 'sha256:' + 'b'.repeat(64),
  secretRef: 'git-auth', chart: '', chartVersion: '', bucketName: '', ...p,
})

const ctx = { edit: vi.fn(async () => {}) } as any
beforeEach(() => { vi.clearAllMocks() })

describe('регистрация пяти источников', () => {
  it('id, вид, view и группа меню; всё уникально и с префиксом flux-', () => {
    for (const [d, kind, id, view] of ALL) {
      expect(d.kind).toBe(kind)
      expect(d.id).toBe(id)
      expect(d.view).toBe(view)
      expect(d.nav?.group).toBe('flux-sources')
      expect(d.namespaced).toBe(true)
    }
    expect(new Set(ALL.map(a => a[0].id)).size).toBe(5)
    expect(new Set(ALL.map(a => a[0].view)).size).toBe(5)
  })

  it('Delete для источников не добавляется: ни действия, ни remove, ни пакетного удаления', () => {
    for (const [d] of ALL) {
      expect(d.actions!.map(a => a.id)).toEqual(['reconcile', 'suspend', 'resume', 'editYaml'])
      expect(d.api.remove).toBeUndefined()
      expect(d.bulkDelete).toBe(false)
    }
  })

  it('права: reconcile — Operate, остальные — Write', () => {
    const a = fluxGitRepository.actions!
    expect(a.find(x => x.id === 'reconcile')!.permission).toBe(Perm.Operate)
    for (const id of ['suspend', 'resume', 'editYaml']) expect(a.find(x => x.id === id)!.permission).toBe(Perm.Write)
  })
})

describe('колонки на фикстурах каждого вида', () => {
  const col = (d: typeof fluxGitRepository, key: string) => d.columns.find(c => c.key === key)!

  it('Git: URL, ветка, короткая ревизия и digest', () => {
    const s = src()
    expect(col(fluxGitRepository, 'url').formatter!(s)).toBe('https://github.com/org/repo')
    expect(col(fluxGitRepository, 'ref').formatter!(s)).toBe('branch: main')
    expect(col(fluxGitRepository, 'revision').formatter!(s)).toBe('main@0123456')
    expect(col(fluxGitRepository, 'digest').formatter!(s)).toBe('sha256:bbbbbbb')
  })
  it('OCI: тег и digest-ревизия', () => {
    const s = src({ kind: 'OCIRepository', url: 'oci://ghcr.io/org/app', refType: 'tag', refValue: '1.2.3', revision: 'sha256:' + 'c'.repeat(64) })
    expect(col(fluxOCIRepository, 'ref').formatter!(s)).toBe('tag: 1.2.3')
    expect(col(fluxOCIRepository, 'revision').formatter!(s)).toBe('sha256:ccccccc')
  })
  it('HelmRepository: только URL, остальное — прочерк', () => {
    const s = src({ kind: 'HelmRepository', url: 'https://charts.example', refType: '', refValue: '', revision: '', digest: '' })
    expect(col(fluxHelmRepository, 'url').formatter!(s)).toBe('https://charts.example')
    for (const k of ['ref', 'revision', 'digest']) expect(col(fluxHelmRepository, k).formatter!(s)).toBe('—')
  })
  it('HelmChart: чарт@версия в колонке ссылки; Bucket: имя бакета', () => {
    expect(col(fluxHelmChart, 'ref').formatter!(src({ kind: 'HelmChart', refType: '', refValue: '', chart: 'podinfo', chartVersion: '6.x' }))).toBe('podinfo@6.x')
    expect(col(fluxBucket, 'ref').formatter!(src({ kind: 'Bucket', refType: '', refValue: '', bucketName: 'artifacts' }))).toBe('artifacts')
  })
  it('пустой DTO не роняет колонки', () => {
    const empty = {} as FluxSourceInfo
    for (const [d] of ALL) for (const c of d.columns) if (c.formatter) expect(() => c.formatter!(empty)).not.toThrow()
  })
  it('цвета состояний', () => {
    const c = fluxGitRepository.columns.find(x => x.key === 'state')!.colorFn!
    expect(c(src({ state: 'Ready' }))).toBe('var(--green)')
    expect(c(src({ state: 'Failed' }))).toBe('var(--red)')
    expect(c(src({ state: 'Suspended' }))).toBe('var(--yellow)')
  })
})

describe('sourceRefText', () => {
  it('тип и значение; только значение; чарт; бакет; пусто', () => {
    expect(sourceRefText({ refType: 'semver', refValue: '>=1.0.0' })).toBe('semver: >=1.0.0')
    expect(sourceRefText({ refValue: 'v1' })).toBe('v1')
    expect(sourceRefText({ chart: 'x', chartVersion: '1' })).toBe('x@1')
    expect(sourceRefText({ bucketName: 'b' })).toBe('b')
    expect(sourceRefText({})).toBe('')
  })
})

describe('данные и действия', () => {
  it('list берёт общий GetFluxSources и оставляет свой вид', async () => {
    vi.mocked(callApp as any).mockResolvedValue([src(), src({ kind: 'Bucket', name: 'b' }), src({ name: 'other' })])
    const list = await fluxGitRepository.api.list(['flux-system'])
    expect(callApp).toHaveBeenCalledWith('GetFluxSources', ['flux-system'])
    expect(list.map(s => s.name)).toEqual(['flux-system', 'other'])
    expect((await fluxBucket.api.list([])).map(s => s.name)).toEqual(['b'])
  })

  it('reconcile и suspend/resume через общие функции', async () => {
    const s = src()
    const a = (id: string) => fluxGitRepository.actions!.find(x => x.id === id)!
    await a('reconcile').run(s, ctx)
    await a('suspend').run(s, ctx)
    await a('resume').run(s, ctx)
    expect(reconcileFlux).toHaveBeenCalledWith([s], { withSource: false })
    expect(setFluxSuspended).toHaveBeenNthCalledWith(1, [s], true)
    expect(setFluxSuspended).toHaveBeenNthCalledWith(2, [s], false)
    expect(a('suspend').when!(src({ suspended: true }))).toBe(false)
    expect(a('resume').when!(src({ suspended: true }))).toBe(true)
  })

  it('YAML через cr:-вид своего plural', async () => {
    await fluxOCIRepository.actions!.find(x => x.id === 'editYaml')!.run(src({ name: 'app' }), ctx)
    expect(ctx.edit).toHaveBeenCalledWith('cr:source.toolkit.fluxcd.io/v1/ocirepositories', 'flux-system', 'app')
  })
})

describe('вкладка «Артефакт»', () => {
  const render = (resource: Partial<FluxSourceInfo>) =>
    renderToString(createSSRApp({ render: () => h(SourceArtifactTab, { resource }) }).use(i18n))

  it('показывает адрес, ссылку, ревизию и digest', async () => {
    const html = await render(src())
    expect(html).toContain('data-testid="flux-artifact"')
    expect(html).toContain('https://github.com/org/repo')
    expect(html).toContain('branch: main')
  })
  it('пустой объект — пустое состояние', async () => {
    expect(await render({})).toContain('data-testid="flux-artifact-empty"')
  })
})

describe('секреты', () => {
  it('в представлении нет ничего, кроме имени секрета: контрольная строка из DTO не размножается', () => {
    const s = src({ url: 'https://github.com/org/repo', secretRef: 'git-auth' })
    const cells = fluxGitRepository.columns.map(c => (c.formatter ? c.formatter(s) : '')).join('|')
    expect(cells).not.toContain('S3CRET')
    expect(cells).not.toContain('git-auth')
  })
})
