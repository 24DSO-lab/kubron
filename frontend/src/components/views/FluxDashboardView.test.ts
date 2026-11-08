// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h, ref, shallowRef } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// Дашборд проверяется на данных фикстуры: композабл подменён, бэкенда нет
const objects = shallowRef<any[] | null>(null)
const loading = ref(false)
const error = ref('')
const state = ref<'ok' | 'unsupportedVersion' | ''>('ok')
vi.mock('../../composables/useFluxObjects', () => ({
  useFluxObjects: () => ({
    objects, loading, refreshing: ref(false), error, updatedAt: ref(null), refresh: vi.fn(),
  }),
}))
vi.mock('../../composables/useFlux', () => ({
  useFlux: () => ({ fluxStatus: ref({ get state() { return state.value } }) }),
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
  objects.value = null
  loading.value = false
  error.value = ''
  state.value = 'ok'
})

describe('FluxDashboardView', () => {
  it('загрузка: индикатор, без пустого состояния', async () => {
    loading.value = true
    const html = await render()
    expect(html).toContain('data-testid="flux-dash-loading"')
    expect(html).not.toContain('data-testid="flux-dash-empty"')
  })

  it('ошибка без данных: сообщение и повтор, дашборд не ломается', async () => {
    error.value = 'forbidden'
    const html = await render()
    expect(html).toContain('data-testid="flux-dash-error"')
    expect(html).toContain('forbidden')
  })

  it('пусто: объектов нет', async () => {
    objects.value = []
    expect(await render()).toContain('data-testid="flux-dash-empty"')
  })

  it('unsupportedVersion: подсказка про Flux 2.3 и flux migrate, данных нет', async () => {
    state.value = 'unsupportedVersion'
    objects.value = []
    const html = await render()
    expect(html).toContain('data-testid="flux-dash-unsupported"')
    expect(html).toContain('flux migrate')
    expect(html).not.toContain('data-testid="flux-dash-stats"')
  })

  it('сводка: пять плиток, приостановленные отдельной плиткой', async () => {
    objects.value = FIXTURE
    const html = await render()
    for (const id of ['ready', 'reconciling', 'failed', 'stalled', 'suspended']) {
      expect(html).toContain(`data-testid="flux-dash-stat-${id}"`)
    }
  })

  it('проблемные: Failed, Stalled, Reconciling, Suspended; здоровые не попадают, Stalled выше Failed', async () => {
    objects.value = FIXTURE
    const html = await render()
    const problems = html.slice(html.indexOf('flux-dash-problems'), html.indexOf('flux-dash-recent'))
    expect(problems.match(/data-testid="flux-dash-problem"/g)).toHaveLength(4)
    expect(problems).not.toContain('ok-1')
    expect(problems.indexOf('stuck')).toBeLessThan(problems.indexOf('broken'))
    expect(problems).toContain('build failed')
  })

  it('всё в порядке: список проблем пуст, остаётся сообщение', async () => {
    objects.value = [o({ name: 'a' }), o({ name: 'b' })]
    const html = await render()
    expect(html).not.toContain('data-testid="flux-dash-problem"')
    expect(html).toContain('flux-empty')
  })

  it('последние reconcile: только объекты с корректным временем', async () => {
    objects.value = FIXTURE
    const html = await render()
    expect(html.match(/data-testid="flux-dash-recent-row"/g)).toHaveLength(1)
  })

  it('источник без ссылки подписан словом, а не пустой строкой', async () => {
    objects.value = [o({ name: 'a' })]
    expect(await render()).not.toContain('title=""')
  })
})
