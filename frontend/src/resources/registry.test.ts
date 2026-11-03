// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'

// registry тянет все определения, включая секции с monaco-editor, который
// не резолвится в node-окружении тестов. Мокаем — на уровне модуля он не вызывается.
vi.mock('monaco-editor', () => ({ editor: {} }))
vi.mock('monaco-editor/editor/editor.api', () => ({ editor: {}, KeyMod: {}, KeyCode: {} }))
vi.mock('monaco-editor/editor/editor.worker?worker', () => ({ default: class {} }))

import { getResourceById, getResourceByView, resources } from './registry'

describe('registry: уникальность идентификаторов', () => {
  it('id всех ресурсов уникальны (дубликат молча перекрывает определение в byId)', () => {
    const ids = resources.map(r => r.id)
    const dupes = ids.filter((v, i) => ids.indexOf(v) !== i)
    expect(new Set(ids).size, `дубликаты id: ${[...new Set(dupes)].join(', ')}`).toBe(ids.length)
  })

  it('view всех ресурсов уникальны (дубликат молча перекрывает определение в byView)', () => {
    const views = resources.map(r => r.view)
    const dupes = views.filter((v, i) => views.indexOf(v) !== i)
    expect(new Set(views).size, `дубликаты view: ${[...new Set(dupes)].join(', ')}`).toBe(views.length)
  })
})

describe('registry: резолвция application', () => {
  // Регрессия: Lens-«Applications» (059e8b3) с id 'application' перекрыл Argo CD Application
  // в byId и спрятал вкладку Graph (b83c122) при открытии детали.
  it("id 'application' резолвится в Argo CD Application с вкладкой Graph", () => {
    const def = getResourceById('application')
    expect(def?.view).toBe('argo-applications')
    expect(def?.kind).toBe('Application')
    expect(def?.tabs?.some(t => t.id === 'argoGraph')).toBe(true)
  })

  it('Lens-«Applications» доступен по своему id и view', () => {
    expect(getResourceById('app-instance')?.kind).toBe('ApplicationInstance')
    expect(getResourceByView('applications')?.id).toBe('app-instance')
  })
})
