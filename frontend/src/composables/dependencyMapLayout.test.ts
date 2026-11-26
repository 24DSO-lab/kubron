// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import ingress from '../components/incidents/__fixtures__/map/ingress.json'
import configmap from '../components/incidents/__fixtures__/map/configmap.json'
import statefulset from '../components/incidents/__fixtures__/map/statefulset.json'
import { buildMapModel, type MapEdge, type MapNode } from './dependencyMap'
import { NODE_H, NODE_W, edgePath, layoutMap } from './dependencyMapLayout'
import type { Report } from '../types/incident'

const model = (r: unknown) => buildMapModel(r as Report)
const mk = (key: string): MapNode => ({ key, group: 'core', kind: 'Pod', namespace: '', name: key, health: 'Healthy', rawHealth: 'Healthy', variant: 'normal', knownKind: true, candidate: false, target: false })
const ed = (from: string, to: string, type: MapEdge['type'] = 'owns'): MapEdge => ({
  key: `${from}|${type}|${to}`, from, to, type, state: 'ok', hierarchy: ['owns', 'routes', 'scales', 'selects'].includes(type),
})

describe('layoutMap', () => {
  it('слои идут по рёбрам иерархии: владелец левее подчинённого', () => {
    const m = model(ingress)
    const { layers } = layoutMap(m.nodes, m.edges)
    const L = (kind: string, name: string) => layers[m.nodes.find((n) => n.kind === kind && n.name === name)!.key]
    expect(L('Ingress', 'web')).toBeLessThan(L('Service', 'web'))
    expect(L('Service', 'web')).toBeLessThan(L('Pod', 'web-1'))
    expect(L('Deployment', 'web')).toBeLessThan(L('ReplicaSet', 'web-7d9'))
    expect(L('ReplicaSet', 'web-7d9')).toBeLessThan(L('Pod', 'web-1'))
  })

  it('вершины вне иерархии ставятся правее источника по прочим рёбрам', () => {
    const m = model(statefulset)
    const { layers } = layoutMap(m.nodes, m.edges)
    const L = (kind: string, name: string) => layers[m.nodes.find((n) => n.kind === kind && n.name === name)!.key]
    expect(L('StorageClass', 'fast')).toBeGreaterThan(L('PersistentVolumeClaim', 'data-db-1'))
    const c = model(configmap)
    const lc = layoutMap(c.nodes, c.edges).layers
    const node = c.nodes.find((n) => n.kind === 'Node')!.key
    const pod = c.nodes.find((n) => n.kind === 'Pod')!.key
    expect(lc[node]).toBeGreaterThan(lc[pod])
  })

  it('детерминирована: порядок входа не влияет на координаты', () => {
    const m = model(configmap)
    const a = layoutMap(m.nodes, m.edges)
    const b = layoutMap([...m.nodes].reverse(), [...m.edges].reverse())
    expect(b).toEqual(a)
  })

  it('вершины не перекрываются, размеры охватывают все вершины', () => {
    const m = model(statefulset)
    const l = layoutMap(m.nodes, m.edges)
    const pts = Object.values(l.positions)
    for (let i = 0; i < pts.length; i++) {
      expect(pts[i].x + NODE_W).toBeLessThanOrEqual(l.width)
      expect(pts[i].y + NODE_H).toBeLessThanOrEqual(l.height)
      for (let j = i + 1; j < pts.length; j++) {
        const overlap = pts[i].x < pts[j].x + NODE_W && pts[j].x < pts[i].x + NODE_W && pts[i].y < pts[j].y + NODE_H && pts[j].y < pts[i].y + NODE_H
        expect(overlap).toBe(false)
      }
    }
  })

  it('малый граф: точная раскладка (инлайн-снимок)', () => {
    const l = layoutMap([mk('a'), mk('b'), mk('c')], [ed('a', 'b'), ed('a', 'c')])
    expect({ layers: l.layers, w: l.width, h: l.height }).toMatchInlineSnapshot(`
      {
        "h": 202,
        "layers": {
          "a": 0,
          "b": 1,
          "c": 1,
        },
        "w": 516,
      }
    `)
  })

  it('цикл в иерархии не зацикливает раскладку', () => {
    const l = layoutMap([mk('a'), mk('b'), mk('c')], [ed('a', 'b'), ed('b', 'c'), ed('c', 'a')])
    expect(Object.keys(l.positions)).toHaveLength(3)
    expect(new Set(Object.values(l.layers)).size).toBe(3)
  })

  it('пустой вход и рёбра к отсутствующим вершинам', () => {
    expect(layoutMap([], [])).toMatchObject({ width: 0, height: 0, layerCount: 0 })
    const l = layoutMap([mk('a')], [ed('a', 'ghost')])
    expect(Object.keys(l.positions)).toEqual(['a'])
  })

  it('1000 вершин раскладываются быстро и без перекрытий по слоям', () => {
    const nodes = [mk('root')]
    const edges: MapEdge[] = []
    for (let i = 0; i < 999; i++) {
      nodes.push(mk(`n${i}`))
      edges.push(ed(i < 30 ? 'root' : `n${i % 30}`, `n${i}`))
    }
    const t0 = performance.now()
    const l = layoutMap(nodes, edges)
    expect(performance.now() - t0).toBeLessThan(3000)
    expect(Object.keys(l.positions)).toHaveLength(1000)
  })
})

describe('edgePath', () => {
  it('прямое, обратное и внутрислойное рёбра дают путь; неизвестная вершина — null', () => {
    const l = layoutMap([mk('a'), mk('b'), mk('c')], [ed('a', 'b'), ed('b', 'c')])
    expect(edgePath(l, 'a', 'b')!.d.startsWith('M ')).toBe(true)
    expect(edgePath(l, 'c', 'a')!.d).toContain('C')
    expect(edgePath(l, 'a', 'zzz')).toBeNull()
    const same = layoutMap([mk('a'), mk('b')], [])
    expect(edgePath(same, 'a', 'b')).not.toBeNull()
  })
})
