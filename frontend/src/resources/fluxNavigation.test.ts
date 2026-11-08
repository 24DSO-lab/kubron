// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'

// registry тянет определения с monaco-editor, который не резолвится в node-окружении (как в registry.test.ts)
vi.mock('monaco-editor', () => ({ editor: {} }))
vi.mock('monaco-editor/editor/editor.api', () => ({ editor: {}, KeyMod: {}, KeyCode: {} }))
vi.mock('monaco-editor/editor/editor.worker?worker', () => ({ default: class {} }))

import { NAV_GROUPS, buildNav, type NavGroup } from './navigation'
import { resources } from './registry'

const fluxGroup = (): NavGroup => {
  const g = buildNav().find((e): e is NavGroup => e.type === 'group' && e.id === 'flux')
  if (!g) throw new Error('группа flux не построена')
  return g
}

describe('каркас меню Flux CD (ADR-0023, раздел 9)', () => {
  it('группа flux стоит после argocd и показывается только при наличии Flux', () => {
    const ids = buildNav().filter((e): e is NavGroup => e.type === 'group').map(g => g.id)
    expect(ids.indexOf('flux')).toBeGreaterThan(ids.indexOf('argocd'))
    expect(NAV_GROUPS.flux.requires).toBe('flux')
    expect(NAV_GROUPS.argocd.requires).toBe('argocd')
  })

  it('в группе дашборд, Kustomizations и HelmReleases', () => {
    expect(fluxGroup().items.map(i => i.view)).toEqual(['flux-dashboard', 'flux-kustomizations', 'flux-helmreleases'])
  })

  it('подгруппы источников, уведомлений и образов свёрнуты и вложены в flux', () => {
    const children = fluxGroup().children
    expect(children.map(c => c.id)).toEqual(['flux-sources', 'flux-notifications', 'flux-images'])
    for (const c of children) {
      expect(NAV_GROUPS[c.id].parent).toBe('flux')
      expect(NAV_GROUPS[c.id].expanded).toBe(false)
      expect(c.requires).toBe('flux')
    }
    expect(children.map(c => c.items.length)).toEqual([5, 3, 3])
  })

  it('все ресурсы Flux имеют id и view с префиксом flux- (чтобы не пересечься с application Argo)', () => {
    const flux = resources.filter(r => r.nav?.group?.startsWith('flux'))
    expect(flux).toHaveLength(13)
    for (const r of flux) {
      expect(r.id.startsWith('flux-'), `id ${r.id}`).toBe(true)
      expect(r.view.startsWith('flux-'), `view ${r.view}`).toBe(true)
    }
  })
})
