// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import type { FluxGraph, FluxGraphEdge, FluxGraphNode } from '../types/flux'
import {
  cleanEdges, collapseKinds, edgePath, graphMode, GROUP_PREFIX, KIND_COLLAPSE_AT, layoutFluxGraph, nodeStateCode, nodeTone,
  worstState,
} from './fluxGraph'

const node = (id: string, p: Partial<FluxGraphNode> = {}): FluxGraphNode => ({
  id, kind: 'Deployment', group: 'apps', version: 'v1', namespace: 'ns', name: id, role: 'resource', state: 'Healthy', ...p,
})
const src = (id = 'src') => node(id, { kind: 'GitRepository', group: 'source.toolkit.fluxcd.io', role: 'source', state: 'Ready' })
const flux = (id = 'ks', p: Partial<FluxGraphNode> = {}) => node(id, { kind: 'Kustomization', group: 'kustomize.toolkit.fluxcd.io', role: 'flux', state: 'Ready', ...p })
const edge = (from: string, to: string, type: FluxGraphEdge['type']): FluxGraphEdge => ({ from, to, type })
const graph = (p: Partial<FluxGraph>): FluxGraph => ({ available: true, remote: false, nodes: [], edges: [], truncated: false, warnings: [], ...p })
const at = (l: ReturnType<typeof layoutFluxGraph>, id: string) => l.nodes.find(n => n.id === id)!

describe('graphMode', () => {
  it('нет данных, недоступно, другой кластер, граф', () => {
    expect(graphMode(null)).toBe('none')
    expect(graphMode(undefined)).toBe('none')
    expect(graphMode(graph({ available: false }))).toBe('unavailable')
    expect(graphMode(graph({ remote: true }))).toBe('remote')
    expect(graphMode(graph({}))).toBe('graph')
  })
  it('недоступность важнее remote', () => {
    expect(graphMode(graph({ available: false, remote: true }))).toBe('unavailable')
  })
})

describe('состояние узла', () => {
  it('код и цвет для состояний Flux и health ресурсов', () => {
    expect(nodeTone({ state: 'Ready' })).toBe('ok')
    expect(nodeTone({ state: 'Healthy' })).toBe('ok')
    expect(nodeTone({ state: 'Progressing' })).toBe('warn')
    expect(nodeTone({ state: 'Suspended' })).toBe('warn')
    expect(nodeTone({ state: 'Degraded' })).toBe('err')
    expect(nodeTone({ state: 'Failed' })).toBe('err')
    expect(nodeTone({ state: 'Stalled' })).toBe('err')
  })
  it('пустое и неизвестное состояние — Unknown и нейтральный цвет', () => {
    for (const s of ['', 'Weird']) {
      expect(nodeStateCode({ state: s })).toBe('Unknown')
      expect(nodeTone({ state: s })).toBe('unk')
    }
  })
  it('worstState выбирает более тяжёлое', () => {
    expect(worstState('Healthy', 'Degraded')).toBe('Degraded')
    expect(worstState('Degraded', 'Healthy')).toBe('Degraded')
    expect(worstState('', 'Healthy')).toBe('Healthy')
  })
})

describe('cleanEdges', () => {
  it('убирает дубли, петли и рёбра к отсутствующим узлам', () => {
    const nodes = [{ id: 'a' }, { id: 'b' }]
    const e = cleanEdges(nodes, [edge('a', 'b', 'owns'), edge('a', 'b', 'owns'), edge('a', 'a', 'owns'), edge('a', 'zzz', 'owns'), edge('a', 'b', 'routes')])
    expect(e.map(x => `${x.from}>${x.to}:${x.type}`)).toEqual(['a>b:owns', 'a>b:routes'])
  })
  it('null и undefined', () => {
    expect(cleanEdges([], null)).toEqual([])
    expect(cleanEdges([], undefined)).toEqual([])
  })
})

describe('layoutFluxGraph: слои «источник → объект → ресурсы»', () => {
  const g = graph({
    nodes: [src(), flux(), node('deploy'), node('rs', { kind: 'ReplicaSet' }), node('pod', { kind: 'Pod' })],
    edges: [edge('src', 'ks', 'sourceRef'), edge('ks', 'deploy', 'inventory'), edge('deploy', 'rs', 'owns'), edge('rs', 'pod', 'owns')],
  })

  it('источник слева, объект Flux следом, ресурсы по глубине владения', () => {
    const l = layoutFluxGraph(g)
    expect(at(l, 'src').layer).toBe(0)
    expect(at(l, 'ks').layer).toBe(1)
    expect(at(l, 'deploy').layer).toBe(2)
    expect(at(l, 'rs').layer).toBe(3)
    expect(at(l, 'pod').layer).toBe(4)
    expect(at(l, 'src').x).toBeLessThan(at(l, 'ks').x)
    expect(at(l, 'ks').x).toBeLessThan(at(l, 'deploy').x)
  })

  it('размеры полотна покрывают все узлы, рёбра получили путь', () => {
    const l = layoutFluxGraph(g)
    for (const n of l.nodes) {
      expect(n.x + n.w).toBeLessThanOrEqual(l.width)
      expect(n.y + n.h).toBeLessThanOrEqual(l.height)
    }
    expect(l.edges).toHaveLength(4)
    for (const e of l.edges) expect(e.d.startsWith('M')).toBe(true)
  })

  it('раскладка не зависит от направления sourceRef в данных', () => {
    const rev = graph({ ...g, edges: [edge('ks', 'src', 'sourceRef'), ...g.edges.slice(1)] })
    const l = layoutFluxGraph(rev)
    expect(at(l, 'src').layer).toBe(0)
    expect(at(l, 'ks').layer).toBe(1)
  })

  it('dependsOn: два объекта Flux в одном слое, связаны дугой', () => {
    const l = layoutFluxGraph(graph({
      nodes: [src(), flux('ks'), flux('base')],
      edges: [edge('src', 'ks', 'sourceRef'), edge('src', 'base', 'sourceRef'), edge('ks', 'base', 'dependsOn')],
    }))
    expect(at(l, 'ks').layer).toBe(1)
    expect(at(l, 'base').layer).toBe(1)
    expect(at(l, 'ks').y).not.toBe(at(l, 'base').y)
    const dep = l.edges.find(e => e.type === 'dependsOn')!
    expect(dep.d).toContain('C')
  })

  it('без inventory: только источник и объект', () => {
    const l = layoutFluxGraph(graph({ nodes: [src(), flux()], edges: [edge('src', 'ks', 'sourceRef')] }))
    expect(l.nodes).toHaveLength(2)
    expect(l.nodes.every(n => n.layer <= 1)).toBe(true)
  })

  it('remote: ресурсов нет, остаётся цепочка источник → объект', () => {
    const l = layoutFluxGraph(graph({ remote: true, nodes: [src(), flux('ks')], edges: [edge('src', 'ks', 'sourceRef')] }))
    expect(l.nodes.map(n => n.role).sort()).toEqual(['flux', 'source'])
  })

  it('порядок внутри слоя: по положению родителей, затем по виду и имени; результат стабилен', () => {
    const nodes = [src(), flux(), node('b'), node('a'), node('c')]
    const edges = [edge('src', 'ks', 'sourceRef'), edge('ks', 'a', 'inventory'), edge('ks', 'b', 'inventory'), edge('ks', 'c', 'inventory')]
    const l1 = layoutFluxGraph(graph({ nodes, edges }))
    const l2 = layoutFluxGraph(graph({ nodes: [...nodes].reverse(), edges: [...edges].reverse() }))
    const order = (l: typeof l1) => l.nodes.filter(n => n.layer === 2).sort((x, y) => x.y - y.y).map(n => n.id)
    expect(order(l1)).toEqual(['a', 'b', 'c'])
    expect(order(l2)).toEqual(order(l1))
  })

  it('цикл владения не зацикливает раскладку', () => {
    const l = layoutFluxGraph(graph({ nodes: [flux(), node('x'), node('y')], edges: [edge('x', 'y', 'owns'), edge('y', 'x', 'owns')] }))
    expect(l.nodes).toHaveLength(3)
  })

  it('пустой, null и неполный граф', () => {
    expect(layoutFluxGraph(null)).toEqual({ nodes: [], edges: [], width: 0, height: 0 })
    expect(layoutFluxGraph(graph({}))).toEqual({ nodes: [], edges: [], width: 0, height: 0 })
    expect(layoutFluxGraph(graph({ nodes: undefined as unknown as FluxGraphNode[], edges: undefined as unknown as FluxGraphEdge[] })).nodes).toEqual([])
  })

  it('не мутирует вход', () => {
    const copy = JSON.stringify(g)
    layoutFluxGraph(g)
    expect(JSON.stringify(g)).toBe(copy)
  })
})

describe('схлопывание одиночных ресурсов одного вида', () => {
  const many = (n: number, kind = 'ConfigMap') => Array.from({ length: n }, (_, i) => node(`${kind}-${i}`, { kind, state: i === 0 ? 'Degraded' : 'Healthy' }))

  it('порог не превышен: узлы остаются', () => {
    const nodes = [flux(), ...many(KIND_COLLAPSE_AT)]
    const r = collapseKinds(nodes, nodes.slice(1).map(n => edge('ks', n.id, 'inventory')))
    expect(r.nodes).toHaveLength(nodes.length)
  })

  it('порог превышен: одна группа с числом, худшим состоянием и общим ребром', () => {
    const cms = many(KIND_COLLAPSE_AT + 3)
    const nodes = [flux(), ...cms]
    const r = collapseKinds(nodes, cms.map(n => edge('ks', n.id, 'inventory')))
    const grp = r.nodes.find(n => n.id === GROUP_PREFIX + 'ConfigMap')!
    expect(grp.count).toBe(KIND_COLLAPSE_AT + 3)
    expect(grp.state).toBe('Degraded')
    expect(grp.members).toHaveLength(KIND_COLLAPSE_AT + 3)
    expect(r.edges).toEqual([{ from: 'ks', to: GROUP_PREFIX + 'ConfigMap', type: 'inventory' }])
  })

  it('развёрнутая группа остаётся отдельными узлами', () => {
    const cms = many(KIND_COLLAPSE_AT + 2)
    const r = collapseKinds([flux(), ...cms], cms.map(n => edge('ks', n.id, 'inventory')), new Set([GROUP_PREFIX + 'ConfigMap']))
    expect(r.nodes.some(n => n.id.startsWith(GROUP_PREFIX))).toBe(false)
  })

  it('ресурсы со связями владения не схлопываются', () => {
    const pods = many(KIND_COLLAPSE_AT + 2, 'Pod')
    const nodes = [flux(), node('rs', { kind: 'ReplicaSet' }), ...pods]
    const edges = [edge('ks', 'rs', 'inventory'), ...pods.map(p => edge('rs', p.id, 'owns'))]
    expect(collapseKinds(nodes, edges).nodes.some(n => n.id.startsWith(GROUP_PREFIX))).toBe(false)
  })

  it('в раскладке группа занимает один узел', () => {
    const cms = many(10)
    const l = layoutFluxGraph(graph({ nodes: [flux(), ...cms], edges: cms.map(n => edge('ks', n.id, 'inventory')) }))
    expect(l.nodes).toHaveLength(2)
    expect(at(l, GROUP_PREFIX + 'ConfigMap').count).toBe(10)
  })
})

describe('edgePath', () => {
  const p = (layer: number, x: number, y: number) => ({ layer, x, y, w: 190, h: 46 }) as any
  it('между слоями — кривая из правого края левого узла в левый край правого, в любом порядке аргументов', () => {
    const a = p(0, 0, 0)
    const b = p(1, 260, 60)
    expect(edgePath(a, b)).toBe(edgePath(b, a))
    expect(edgePath(a, b).startsWith('M190,23')).toBe(true)
    expect(edgePath(a, b).endsWith('260,83')).toBe(true)
  })
  it('в одном слое — дуга справа', () => {
    expect(edgePath(p(1, 260, 0), p(1, 260, 60))).toContain('C')
  })
})
