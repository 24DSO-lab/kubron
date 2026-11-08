// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// Состояние вынесено в hoisted-контекст: фабрики vi.mock выполняются до импорта модулей.
const mockState = vi.hoisted(() => ({
  objects: null as any[] | null,
  loading: false,
  error: '',
  state: 'ok' as 'ok' | 'unsupportedVersion' | '',
  refresh: vi.fn(),
}))

vi.mock('../../composables/useFluxObjects', async () => {
  const vue = await vi.importActual<typeof import('vue')>('vue')
  return {
    useFluxObjects: () => ({
      objects: vue.computed(() => mockState.objects),
      loading: vue.computed(() => mockState.loading),
      refreshing: vue.computed(() => false),
      error: vue.computed(() => mockState.error),
      updatedAt: vue.computed(() => null),
      refresh: mockState.refresh,
    }),
  }
})

vi.mock('../../composables/useFlux', async () => {
  const vue = await vi.importActual<typeof import('vue')>('vue')
  return {
    useFlux: () => ({
      fluxStatus: vue.computed(() => ({ state: mockState.state })),
    }),
  }
})

// resourceActions импортирует registry, который транзитивно загружает browser-only Monaco.
vi.mock('../../resources/actions', () => ({
  resourceActions: { open: vi.fn() },
}))

import { i18n } from '../../i18n'
import FluxDashboardView from './FluxDashboardView.vue'

const render = () => renderToString(createSSRApp({ render: () => h(FluxDashboardView, { clusterName: 'c1' }) }).use(i18n))

const o = (p: Record<string, unknown>) => ({
  kind: 'Kustomization', name: 'app', namespace: 'flux-system', state: 'Ready', message: '', revision: '', lastReconcileAt: '', sourceRef: null, ...p,
})

const FIXTURE = [
  o({ name: 'ok-1', lastReconcileAt: '2026-11-07T10:00:00Z', sourceRef: { kind: 'GitRepository', namespace: '', name: 'repo' } }),
  o({ name: 'ok-2' }),
  o({ name: 'broken', state: 'Failed', message: 'build failed' }),
  o({ name: 'stuck', state: 'Stalled' }),
  o({ name: 'syncing', state: 'Reconciling' }),
  o({ kind: 'HelmRelease', name: 'paused', namespace: 'apps', state: 'Suspended' }),
]

beforeEach(() => {
  mockState.objects = null
  mockState.loading = false
  mockState.error = ''
  mockState.state = 'ok'
  mockState.refresh.mockReset()
})

describe('FluxDashboardView', () => {
  it('загрузка: индикатор, без пустого состояния', async () => {
    mockState.loading = true
    const html = await render()
    expect(html).toContain('data-testid="flux-dash-loading"')
    expect(html).not.toContain('data-testid="flux-dash-empty"')
  })

  it('ошибка без данных: сообщение и повтор, дашборд не ломается', async () => {
    mockState.error = 'forbidden'
    const html = await render()
    expect(html).toContain('data-testid="flux-dash-error"')
    expect(html).toContain('forbidden')
  })

  it('пусто: объектов нет', async () => {
    mockState.objects = []
    expect(await render()).toContain('data-testid="flux-dash-empty"')
  })

  it('unsupportedVersion: подсказка про Flux 2.3 и flux migrate, данных нет', async () => {
    mockState.state = 'unsupportedVersion'
    mockState.objects = []
    const html = await render()
    expect(html).toContain('data-testid="flux-dash-unsupported"')
    expect(html).toContain('flux migrate')
    expect(html).not.toContain('data-testid="flux-dash-stats"')
  })

  it('сводка: пять плиток, приостановленные отдельной плиткой', async () => {
    mockState.objects = FIXTURE
    const html = await render()
    for (const id of ['ready', 'reconciling', 'failed', 'stalled', 'suspended']) {
      expect(html).toContain(`data-testid="flux-dash-stat-${id}"`)
    }
  })

  it('проблемные: Failed, Stalled, Reconciling, Suspended; здоровые не попадают, Stalled выше Failed', async () => {
    mockState.objects = FIXTURE
    const html = await render()
    const problems = html.slice(html.indexOf('flux-dash-problems'), html.indexOf('flux-dash-recent'))
    expect(problems.match(/data-testid="flux-dash-problem"/g)).toHaveLength(4)
    expect(problems).not.toContain('ok-1')
    expect(problems.indexOf('stuck')).toBeLessThan(problems.indexOf('broken'))
    expect(problems).toContain('build failed')
  })

  it('всё в порядке: список проблем пуст, остаётся сообщение', async () => {
    mockState.objects = [o({ name: 'a' }), o({ name: 'b' })]
    const html = await render()
    expect(html).not.toContain('data-testid="flux-dash-problem"')
    expect(html).toContain('flux-empty')
  })

  it('последние reconcile: только объекты с корректным временем', async () => {
    mockState.objects = FIXTURE
    const html = await render()
    expect(html.match(/data-testid="flux-dash-recent-row"/g)).toHaveLength(1)
  })

  it('источник без ссылки подписан словом, а не пустой строкой', async () => {
    mockState.objects = [o({ name: 'a' })]
    expect(await render()).not.toContain('title=""')
  })
})
