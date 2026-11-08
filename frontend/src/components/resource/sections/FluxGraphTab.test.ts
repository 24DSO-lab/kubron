// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// SSR вызывает watch с immediate один раз: запрос графа стартует, ответ задаёт тест
let reply: Promise<unknown> = new Promise(() => {})
vi.mock('../../../resources/wails', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../resources/wails')>()),
  callApp: vi.fn(() => reply),
}))

// Реестр ресурсов импортирует Monaco для редактора; для SSR этого теста достаточно изолировать lookup.
vi.mock('../../../resources/registry', () => ({
  getResourceById: vi.fn(),
}))

import { callApp } from '../../../resources/wails'
import { i18n } from '../../../i18n'
import type { FluxObjectInfo } from '../../../types/flux'
import FluxGraphTab from './FluxGraphTab.vue'

const resource: Partial<FluxObjectInfo> = { kind: 'Kustomization', namespace: 'apps', name: 'web' }
const render = (r: Partial<FluxObjectInfo> = resource) =>
  renderToString(createSSRApp({ render: () => h(FluxGraphTab, { resource: r }) }).use(i18n))

beforeEach(() => {
  vi.clearAllMocks()
  reply = new Promise(() => {})
})

describe('FluxGraphTab', () => {
  it('пока граф загружается — индикатор, а не пустой граф', async () => {
    const html = await render()
    expect(html).toContain('data-testid="flux-graph-loading"')
    expect(html).not.toContain('data-testid="flux-graph-empty"')
  })

  it('запрос идёт по ссылке на объект: вид, namespace, имя', async () => {
    await render()
    expect(callApp).toHaveBeenCalledWith('GetFluxGraph', { kind: 'Kustomization', namespace: 'apps', name: 'web' })
  })

  it('объект без имени: запроса нет', async () => {
    await render({})
    expect(callApp).not.toHaveBeenCalled()
  })
})
