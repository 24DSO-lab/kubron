// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import ingress from '../components/incidents/__fixtures__/map/ingress.json'
import hpa from '../components/incidents/__fixtures__/map/hpa.json'
import configmap from '../components/incidents/__fixtures__/map/configmap.json'
import statefulset from '../components/incidents/__fixtures__/map/statefulset.json'
import batch from '../components/incidents/__fixtures__/map/batch.json'
import truncated from '../components/incidents/__fixtures__/map/truncated.json'
import legacy from '../components/incidents/__fixtures__/map/legacy-no-graph.json'
import emptyGraph from '../components/incidents/__fixtures__/map/empty-graph.json'
import {
  MAP_COLLAPSE_THRESHOLD, buildMapModel, collapseModel, filterModel, findingsFor, groupKey, highlightChains, isGroupKey, navigate,
  relationsOf, shortestPath, worstHealth, type MapModel,
} from './dependencyMap'
import { layoutMap } from './dependencyMapLayout'
import type { Report } from '../types/incident'

const rep = (r: unknown) => r as Report
const find = (m: MapModel, kind: string, name: string) => m.nodes.find((n) => n.kind === kind && n.name === name)!

describe('buildMapModel: нет графа', () => {
  it('отчёт без graph и null-срезы дают пустую карту без ошибки', () => {
    for (const r of [rep(legacy), rep(emptyGraph), null, undefined]) {
      const m = buildMapModel(r)
      expect(m.empty).toBe(true)
      expect(m.nodes).toEqual([])
    }
  })
})

describe('buildMapModel: фикстуры D1–D6', () => {
  it('Ingress: заглушки Missing, ребро routes в Missing, кандидат и цель', () => {
    const m = buildMapModel(rep(ingress))
    expect(m.empty).toBe(false)
    expect(find(m, 'Service', 'api').variant).toBe('placeholder')
    expect(find(m, 'IngressClass', 'nginx').variant).toBe('placeholder')
    expect(m.edges.find((e) => e.type === 'routes' && e.state === 'missing')).toBeTruthy()
    expect(find(m, 'Ingress', 'web').candidate).toBe(true)
    expect(find(m, 'Ingress', 'web').target).toBe(true)
    expect(m.targetKey).toBe('networking.k8s.io/Ingress/shop/web')
  })

  it('HPA: цель scales отсутствует', () => {
    const m = buildMapModel(rep(hpa))
    expect(find(m, 'Deployment', 'web-old').health).toBe('Missing')
    expect(m.edges[0]).toMatchObject({ type: 'scales', state: 'missing', hierarchy: true })
  })

  it('ConfigMap/Secret: Secret — особый вариант, optional-ConfigMap с ребром ok, заглушка с ребром missing', () => {
    const m = buildMapModel(rep(configmap))
    const secret = find(m, 'Secret', 'db-creds')
    expect(secret).toMatchObject({ variant: 'secret', health: 'Unknown', unknown: 'policy' })
    expect(find(m, 'ConfigMap', 'app-config').variant).toBe('placeholder')
    const optional = find(m, 'ConfigMap', 'feature-flags')
    expect(m.edges.find((e) => e.to === optional.key)?.state).toBe('ok')
    expect(m.edges.find((e) => e.to === find(m, 'ConfigMap', 'app-config').key)?.state).toBe('missing')
  })

  it('StatefulSet: PVC-заглушка и StorageClass, состав видов для фильтров', () => {
    const m = buildMapModel(rep(statefulset))
    expect(find(m, 'PersistentVolumeClaim', 'data-db-2').variant).toBe('placeholder')
    expect(m.kinds).toEqual(['PersistentVolumeClaim', 'Pod', 'StatefulSet', 'StorageClass'])
    expect(m.healths[0]).toBe('Missing') // самые тяжёлые состояния — первыми
  })

  it('Job/CronJob: Suspended не считается проблемой-заглушкой', () => {
    const m = buildMapModel(rep(batch))
    expect(find(m, 'CronJob', 'nightly')).toMatchObject({ health: 'Suspended', variant: 'normal' })
    expect(m.candidateKeys).toHaveLength(1)
  })
})

describe('buildMapModel: устойчивость', () => {
  it('усечённый граф: неполный, неизвестные вид/состояние/тип ребра нейтральны, висячее ребро отброшено', () => {
    const m = buildMapModel(rep(truncated))
    expect(m.incomplete).toBe(true)
    expect(m.droppedEdges).toBe(1)
    const widget = find(m, 'Widget', 'w1')
    expect(widget.knownKind).toBe(false)
    expect(find(m, 'Pod', 'p2').health).toBe('other')
    expect(find(m, 'Gadget', 'g1')).toMatchObject({ variant: 'unknown', unknown: 'other' })
    expect(m.edges.find((e) => e.to === widget.key)).toMatchObject({ type: 'other', hierarchy: false })
  })

  it('«Граф неполный» и по пометке Graph в Partial отчёта', () => {
    const r = rep({ ...(ingress as object), partial: ['Graph'] })
    expect(buildMapModel(r).incomplete).toBe(true)
    expect(buildMapModel(rep(ingress)).incomplete).toBe(false)
  })

  it('дубликаты вершин и рёбер схлопываются; побеждает более тяжёлое состояние', () => {
    const r = rep({ ...(hpa as object), graph: { nodes: [
      { kind: 'Pod', name: 'a', namespace: 'x', group: 'core', health: 'Healthy' },
      { kind: 'Pod', name: 'a', namespace: 'x', health: 'Degraded' },
    ], edges: [
      { from: 'core/Pod/x/a', to: 'core/Pod/x/a', type: 'owns', state: 'ok' },
    ] } })
    const m = buildMapModel(r)
    expect(m.nodes).toHaveLength(1)
    expect(m.nodes[0].health).toBe('Degraded')
    expect(m.edges).toHaveLength(0) // петля отброшена
  })

  it('результат не зависит от порядка входных вершин и рёбер', () => {
    const source = rep(ingress)
    const shuffled = rep({ ...(ingress as object), graph: { nodes: [...source.graph!.nodes!].reverse(), edges: [...source.graph!.edges!].reverse() } })
    expect(buildMapModel(shuffled)).toEqual(buildMapModel(rep(ingress)))
  })
})

describe('цепочки и находки', () => {
  it('подсветка идёт по report.chains', () => {
    const m = buildMapModel(rep(ingress))
    const h = highlightChains(m, rep(ingress))
    expect(h.nodes.has(find(m, 'Service', 'api').key)).toBe(true)
    expect(h.nodes.has(find(m, 'Pod', 'web-1').key)).toBe(true)
    expect(h.nodes.has(find(m, 'IngressClass', 'nginx').key)).toBe(false)
    expect(h.edges.size).toBe(3)
  })

  it('без chains путь от цели до кандидата ищется по графу', () => {
    const r = rep({ ...(statefulset as object), chains: undefined })
    const m = buildMapModel(r)
    // Кандидат — сам StatefulSet (цель): путь из одной вершины.
    expect(highlightChains(m, r).nodes.size).toBe(1)
    expect(shortestPath(m, find(m, 'Pod', 'db-1').key, find(m, 'StorageClass', 'fast').key)).toHaveLength(3) // Pod → PVC → StorageClass: Pod монтирует PVC напрямую
  })

  it('находки и связи вершины', () => {
    const m = buildMapModel(rep(ingress))
    const f = findingsFor(rep(ingress), find(m, 'Ingress', 'web').key)
    expect(f.facts).toHaveLength(3)
    expect(f.hypotheses).toHaveLength(3)
    expect(findingsFor(rep(ingress), 'group:Pod').facts).toEqual([])
    const rel = relationsOf(m, find(m, 'Ingress', 'web').key)
    expect(rel.filter((r) => r.direction === 'out')).toHaveLength(3)
  })
})

describe('фильтры', () => {
  it('фильтр по виду скрывает вершины и их рёбра, но не цель и кандидатов', () => {
    const m = buildMapModel(rep(statefulset))
    const f = filterModel(m, { kinds: new Set(['Pod']), healths: null })
    expect(f.nodes.map((n) => n.kind).sort()).toEqual(['Pod', 'Pod', 'StatefulSet'])
    expect(f.edges.every((e) => f.nodes.some((n) => n.key === e.from) && f.nodes.some((n) => n.key === e.to))).toBe(true)
  })

  it('фильтр по состоянию; keep удерживает вершину', () => {
    const m = buildMapModel(rep(statefulset))
    const pod0 = find(m, 'Pod', 'db-0').key
    const f = filterModel(m, { kinds: null, healths: new Set(['Missing']) }, new Set([pod0]))
    expect(f.nodes.some((n) => n.key === pod0)).toBe(true)
    expect(f.nodes.filter((n) => n.health === 'Healthy')).toHaveLength(1)
  })

  it('без активных фильтров возвращается та же модель', () => {
    const m = buildMapModel(rep(batch))
    expect(filterModel(m, { kinds: null, healths: null })).toBe(m)
  })
})

function bigReport(n: number): Report {
  const nodes = [{ kind: 'Deployment', name: 'web', namespace: 'p', group: 'apps', health: 'Degraded' }]
  const edges = []
  for (let i = 0; i < n - 2; i++) {
    nodes.push({ kind: 'Pod', name: `p${i}`, namespace: 'p', group: 'core', health: i === 7 ? 'Degraded' : 'Healthy' })
    edges.push({ from: 'apps/Deployment/p/web', to: `core/Pod/p/p${i}`, type: 'owns', state: 'ok' })
  }
  nodes.push({ kind: 'Node', name: 'n1', namespace: '', group: 'core', health: 'Healthy' })
  for (let i = 0; i < n - 2; i++) edges.push({ from: `core/Pod/p/p${i}`, to: 'core//Node//n1'.replace('//Node//', '/Node//'), type: 'runs_on', state: 'ok' })
  return { ruleSetVersion: 2, target: { group: 'apps', kind: 'Deployment', namespace: 'p', name: 'web' }, observedAt: '', symptoms: [], resources: [],
    facts: [], hypotheses: [], recommendations: [], steps: [], missing: [], graph: { nodes, edges } } as unknown as Report
}

describe('свёртка больших графов', () => {
  it('малый граф не сворачивается', () => {
    const m = buildMapModel(rep(ingress))
    expect(collapseModel(m)).toBe(m)
  })

  it('1000 вершин: здоровые Pod сливаются в группу, проблемный остаётся, рёбра дедуплицируются', () => {
    const m = buildMapModel(bigReport(1000))
    expect(m.nodes.length).toBe(1000)
    const c = collapseModel(m)
    expect(c.nodes.length).toBeLessThan(MAP_COLLAPSE_THRESHOLD)
    const g = c.nodes.find((n) => n.key === groupKey('Pod'))!
    expect(g.variant).toBe('group')
    expect(g.members).toHaveLength(997) // 998 Pod, один Degraded остаётся отдельно
    expect(c.nodes.some((n) => n.name === 'p7')).toBe(true)
    expect(c.edges.filter((e) => e.to === g.key)).toHaveLength(1)
    expect(isGroupKey(g.key)).toBe(true)
  })

  it('раскрытие вида и keep возвращают вершины', () => {
    const m = buildMapModel(bigReport(300))
    expect(collapseModel(m, new Set(['Pod'])).nodes.length).toBe(300)
    const keepKey = 'core/Pod/p/p3'
    const c = collapseModel(m, new Set(), new Set([keepKey]))
    expect(c.nodes.some((n) => n.key === keepKey)).toBe(true)
  })

  it('худшее состояние группы', () => {
    expect(worstHealth(['Healthy', 'Degraded', 'Unknown'])).toBe('Degraded')
    expect(worstHealth([])).toBe('Healthy')
  })
})

describe('клавиатурная навигация', () => {
  const m = buildMapModel(rep(ingress))
  const { positions } = layoutMap(m.nodes, m.edges)
  const ing = find(m, 'Ingress', 'web').key

  it('вправо — сосед в следующем слое, влево — обратно', () => {
    const right = navigate(positions, ing, 'ArrowRight')!
    expect(positions[right].x).toBeGreaterThan(positions[ing].x)
    expect(navigate(positions, right, 'ArrowLeft')).not.toBe(right)
  })

  it('вверх/вниз ходят по одному слою и не покидают его границ', () => {
    const col = Object.keys(positions).filter((k) => positions[k].x === positions[ing].x).sort((a, b) => positions[a].y - positions[b].y)
    expect(navigate(positions, col[0], 'ArrowUp')).toBe(col[0])
    if (col.length > 1) expect(navigate(positions, col[0], 'ArrowDown')).toBe(col[1])
  })

  it('Home/End, пустая карта и отсутствие текущей вершины', () => {
    const keys = Object.keys(positions)
    expect(keys).toContain(navigate(positions, null, 'Home')!)
    expect(navigate(positions, null, 'ArrowDown')).toBe(navigate(positions, null, 'Home'))
    expect(navigate({}, null, 'End')).toBeNull()
  })
})
