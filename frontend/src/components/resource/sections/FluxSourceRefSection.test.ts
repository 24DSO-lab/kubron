// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'

import { describe, expect, it, vi } from 'vitest'

// Registry imports resource definitions that transitively load Monaco Editor.
// Keep these SSR tests in Node without evaluating Monaco's browser-only modules.
vi.mock('monaco-editor', () => ({ editor: {} }))
vi.mock('monaco-editor/editor/editor.api', () => ({ editor: {}, KeyMod: {}, KeyCode: {} }))
vi.mock('monaco-editor/editor/editor.worker?worker', () => ({ default: class {} }))

import { i18n } from '../../../i18n'
import type { FluxObjectInfo, FluxRef } from '../../../types/flux'
import FluxSourceRefSection from './FluxSourceRefSection.vue'
import { fluxResourceId, refLabel, refTarget } from './fluxSourceRef'


const ref = (p: Partial<FluxRef>): FluxRef => ({ kind: 'GitRepository', namespace: 'flux-system', name: 'repo', ...p })
const render = (resource: Partial<FluxObjectInfo>) =>
  renderToString(createSSRApp({ render: () => h(FluxSourceRefSection, { resource }) }).use(i18n))

describe('fluxResourceId и refLabel', () => {
  it('id определения — flux-<kind в нижнем регистре>', () => {
    expect(fluxResourceId('GitRepository')).toBe('flux-gitrepository')
    expect(fluxResourceId('OCIRepository')).toBe('flux-ocirepository')
    expect(fluxResourceId('HelmRelease')).toBe('flux-helmrelease')
  })
  it('подпись: Kind ns/name; без вида — ns/name; без namespace — имя', () => {
    expect(refLabel(ref({}))).toBe('GitRepository flux-system/repo')
    expect(refLabel(ref({ kind: '' }))).toBe('flux-system/repo')
    expect(refLabel(ref({ kind: '', namespace: '' }))).toBe('repo')
  })
})

describe('refTarget', () => {
  const known = (id: string) => id === 'flux-gitrepository' || id === 'flux-kustomization'
  const self = { kind: 'Kustomization', namespace: 'apps' }

  it('источник: вид и namespace из ссылки', () => {
    expect(refTarget(ref({}), self, known)).toEqual({ id: 'flux-gitrepository', ns: 'flux-system', name: 'repo' })
  })
  it('dependsOn: вид и пустой namespace берутся у самого объекта', () => {
    expect(refTarget({ kind: '', namespace: '', name: 'base' }, self, known)).toEqual({ id: 'flux-kustomization', ns: 'apps', name: 'base' })
  })
  it('вид без определения в приложении — перехода нет', () => {
    expect(refTarget(ref({ kind: 'Bucket' }), self, known)).toBeNull()
  })
  it('неполная ссылка — перехода нет, без исключения', () => {
    expect(refTarget(ref({ name: '' }), self, known)).toBeNull()
    expect(refTarget({ kind: '', namespace: '', name: 'x' }, {}, known)).toBeNull()
  })
})

describe('FluxSourceRefSection', () => {
  it('источник и зависимости выводятся списком', async () => {
    const html = await render({
      kind: 'Kustomization', namespace: 'apps',
      sourceRef: ref({}),
      dependsOn: [{ kind: '', namespace: 'infra', name: 'base' }, { kind: '', namespace: '', name: 'crds' }],
    })
    expect(html).toContain('data-testid="flux-sourceref-source"')
    expect(html).toContain('GitRepository flux-system/repo')
    expect(html).toContain('infra/base')
    expect(html).toContain('data-testid="flux-sourceref-deps"')
  })

  it('источника и зависимостей нет — пустые подписи', async () => {
    const html = await render({ kind: 'Kustomization', namespace: 'apps', sourceRef: null, dependsOn: [] })
    expect(html).toContain('data-testid="flux-sourceref-none"')
    expect(html).toContain('data-testid="flux-sourceref-nodeps"')
  })

  it('отсутствующие поля не ломают рендер', async () => {
    const html = await render({})
    expect(html).toContain('data-testid="flux-sourceref-none"')
    expect(html).toContain('data-testid="flux-sourceref-nodeps"')
  })

  it('переход оформлен кнопкой (доступен с клавиатуры) только для известных видов', async () => {
    const html = await render({ kind: 'Kustomization', namespace: 'apps', sourceRef: ref({}) })
    // flux-gitrepository зарегистрирован в реестре задачей 0.2
    expect(html).toContain('<button')
    const unknown = await render({ kind: 'Kustomization', namespace: 'apps', sourceRef: ref({ kind: 'Mystery' }) })
    expect(unknown).not.toContain('<button')
  })
})
