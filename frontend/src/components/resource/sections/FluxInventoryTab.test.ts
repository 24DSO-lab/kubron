// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

// Бэкенд недоступен: запрос графа не завершается. Остальные экспорты модуля нужны реестру ресурсов, поэтому
// подменяется только callApp.
vi.mock('../../../resources/wails', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../resources/wails')>()),
  callApp: vi.fn(() => new Promise(() => {})),
}))

import { i18n } from '../../../i18n'
import type { FluxGraph, FluxGraphNode, FluxObjectInfo } from '../../../types/flux'
import FluxInventoryTab, { inventoryMode, resourceNodes, splitInventory } from './FluxInventoryTab.vue'

const node = (p: Partial<FluxGraphNode>): FluxGraphNode => ({
  id: `apps/Deployment/ns/${p.name ?? 'x'}`, kind: 'Deployment', group: 'apps', version: 'v1', namespace: 'ns', name: 'x', role: 'resource', state: 'Healthy', ...p,
})
const graph = (p: Partial<FluxGraph>): FluxGraph => ({ available: true, remote: false, nodes: [], edges: [], truncated: false, warnings: [], ...p })

describe('inventoryMode', () => {
  it('нет данных, недоступно (Flux < 2.8), другой кластер, список', () => {
    expect(inventoryMode(null)).toBe('none')
    expect(inventoryMode(undefined)).toBe('none')
    expect(inventoryMode(graph({ available: false }))).toBe('unavailable')
    expect(inventoryMode(graph({ remote: true }))).toBe('remote')
    expect(inventoryMode(graph({}))).toBe('list')
  })
  it('недоступность важнее remote', () => {
    expect(inventoryMode(graph({ available: false, remote: true }))).toBe('unavailable')
  })
})

describe('resourceNodes', () => {
  it('оставляет только узлы с ролью resource', () => {
    const g = graph({ nodes: [node({ name: 'a' }), node({ name: 'src', role: 'source' }), node({ name: 'ks', role: 'flux' })] })
    expect(resourceNodes(g).map(n => n.name)).toEqual(['a'])
  })
  it('пустой и отсутствующий граф', () => {
    expect(resourceNodes(null)).toEqual([])
    expect(resourceNodes(graph({ nodes: undefined as unknown as FluxGraphNode[] }))).toEqual([])
  })
})

describe('splitInventory', () => {
  const nodes = [
    node({ name: 'cfg', kind: 'ConfigMap' }),
    node({ name: 'web', kind: 'Deployment' }),
    node({ name: 'db', kind: 'StatefulSet', namespace: 'data' }),
    node({ name: 'svc', kind: 'Service' }),
    node({ name: 'job', kind: 'CronJob' }),
  ]

  it('делит на рабочие нагрузки и прочее, внутри — по kind, namespace, имени', () => {
    const s = splitInventory(nodes)
    expect(s.workloads.map(n => n.name)).toEqual(['job', 'web', 'db'])
    expect(s.others.map(n => n.name)).toEqual(['cfg', 'svc'])
  })
  it('фильтр по kind', () => {
    const s = splitInventory(nodes, '', 'Service')
    expect(s.workloads).toEqual([])
    expect(s.others.map(n => n.name)).toEqual(['svc'])
  })
  it('поиск по kind, имени и namespace без учёта регистра и пробелов по краям', () => {
    expect(splitInventory(nodes, '  DATA ').workloads.map(n => n.name)).toEqual(['db'])
    expect(splitInventory(nodes, 'configmap').others.map(n => n.name)).toEqual(['cfg'])
    expect(splitInventory(nodes, 'nothing')).toEqual({ workloads: [], others: [] })
  })
  it('не мутирует входной массив', () => {
    const copy = [...nodes]
    splitInventory(nodes)
    expect(nodes).toEqual(copy)
  })
})

describe('FluxInventoryTab', () => {
  it('пока граф загружается, показывает индикатор, а не пустой список', async () => {
    const resource: Partial<FluxObjectInfo> = { kind: 'Kustomization', namespace: 'apps', name: 'web' }
    const html = await renderToString(createSSRApp({ render: () => h(FluxInventoryTab, { resource }) }).use(i18n))
    expect(html).toContain('data-testid="flux-inventory-loading"')
    expect(html).not.toContain('data-testid="flux-inventory-empty"')
  })

  it('объект без имени: нет запроса и нет индикатора', async () => {
    const html = await renderToString(createSSRApp({ render: () => h(FluxInventoryTab, { resource: {} }) }).use(i18n))
    expect(html).not.toContain('data-testid="flux-inventory-loading"')
    expect(html).toContain('data-testid="flux-inventory-empty"')
  })
})
