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
  MAP_COLLAPSE_THRESHOLD, buildMapModel, collapseModel, filterModel, findingsFor, groupKey, groupKeyOf, groupKeysOf, highlightChains, isGroupKey, navigate,
  relationsOf, shortestPath, worstHealth, type MapModel,
} from './dependencyMap'
import { layoutMap } from './dependencyMapLayout'
import type { GraphEdge, GraphNode, Report } from '../types/incident'

/**
 * JSON-фикстуры выводятся TS как объединение форм (у одного факта `params.service`, у другого `params.class`),
 * поэтому прямое приведение к `Report` невозможно. Вход принимается как `unknown` и проверяется на форму
 * type guard'ом — одиночное сужение вместо двойного приведения.
 */
function isReport(r: unknown): r is Report {
  if (typeof r !== 'object' || r === null) return false
  const o = r as Record<string, unknown>
  return typeof o.target === 'object' && o.target !== null && (o.graph === undefined || (typeof o.graph === 'object' && o.graph !== null))
}
const rep = (r: unknown): Report => {
  if (!isReport(r)) throw new Error('fixture is not a Report')
  return r
}
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
    const shuffled = rep({ ...(ingress as object), graph: { nodes: [...rep(ingress).graph!.nodes!].reverse(), edges: [...rep(ingress).graph!.edges!].reverse() } })
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
    expect(findingsFor(rep(ingress), groupKey('Pod', 'shop')).facts).toEqual([])
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

/** Отчёт с графом из готовых вершин и рёбер; цель — первая вершина. Без приведений типов. */
function reportOf(nodes: GraphNode[], edges: GraphEdge[]): Report {
  const t = nodes[0]
  return {
    ruleSetVersion: 2, target: { group: t.group, kind: t.kind, namespace: t.namespace, name: t.name }, observedAt: '', symptoms: [], resources: [],
    facts: [], hypotheses: [], recommendations: [], steps: [], missing: [], graph: { nodes, edges },
  }
}

const edge = (from: string, to: string, type = 'owns', state = 'ok'): GraphEdge => ({ from, to, type, state })

function bigReport(n: number): Report {
  const nodes: GraphNode[] = [{ kind: 'Deployment', name: 'web', namespace: 'p', group: 'apps', health: 'Degraded' }]
  const edges: GraphEdge[] = []
  for (let i = 0; i < n - 2; i++) {
    nodes.push({ kind: 'Pod', name: `p${i}`, namespace: 'p', group: 'core', health: i === 7 ? 'Degraded' : 'Healthy' })
    edges.push(edge('apps/Deployment/p/web', `core/Pod/p/p${i}`))
  }
  nodes.push({ kind: 'Node', name: 'n1', namespace: '', group: 'core', health: 'Healthy' })
  for (let i = 0; i < n - 2; i++) edges.push(edge(`core/Pod/p/p${i}`, 'core/Node//n1', 'runs_on'))
  return reportOf(nodes, edges)
}

/** Граф «Deployment → Pod-ы» в нескольких namespace: по `perNs` здоровых Pod в каждом, плюс кластерные Node. */
function multiNsReport(namespaces: string[], perNs: number, clusterNodes = 0): Report {
  const nodes: GraphNode[] = [{ kind: 'Deployment', name: 'web', namespace: namespaces[0], group: 'apps', health: 'Degraded' }]
  const edges: GraphEdge[] = []
  for (const ns of namespaces) {
    for (let i = 0; i < perNs; i++) {
      nodes.push({ kind: 'Pod', name: `p${i}`, namespace: ns, group: 'core', health: 'Healthy' })
      edges.push(edge(`apps/Deployment/${namespaces[0]}/web`, `core/Pod/${ns}/p${i}`))
    }
  }
  for (let i = 0; i < clusterNodes; i++) nodes.push({ kind: 'Node', name: `n${i}`, group: 'core', health: 'Healthy' })
  return reportOf(nodes, edges)
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
    const g = c.nodes.find((n) => n.key === groupKey('Pod', 'p'))!
    expect(g.variant).toBe('group')
    expect(g.members).toHaveLength(997) // 998 Pod, один Degraded остаётся отдельно
    expect(c.nodes.some((n) => n.name === 'p7')).toBe(true)
    expect(c.edges.filter((e) => e.to === g.key)).toHaveLength(1)
    expect(isGroupKey(g.key)).toBe(true)
  })

  it('раскрытие вида и keep возвращают вершины', () => {
    const m = buildMapModel(bigReport(300))
    expect(collapseModel(m, new Set([groupKey('Pod', 'p')])).nodes.length).toBe(300)
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

describe('группировка: вид + namespace', () => {
  const ns2 = buildMapModel(multiNsReport(['a', 'b'], 60))

  it('один вид в разных namespace даёт разные группы с namespace-контекстом', () => {
    expect(ns2.nodes).toHaveLength(121)
    const c = collapseModel(ns2)
    const groups = c.nodes.filter((n) => n.variant === 'group')
    expect(groups.map((g) => g.key)).toEqual([groupKey('Pod', 'a'), groupKey('Pod', 'b')])
    for (const g of groups) {
      expect(g.members).toHaveLength(60)
      expect(g.kind).toBe('Pod')
      expect(g.members!.every((k) => k.startsWith(`core/Pod/${g.namespace}/`))).toBe(true)
    }
    expect(groups.map((g) => g.namespace)).toEqual(['a', 'b'])
  })

  it('cluster-scoped ресурсы группируются отдельно, без искусственного namespace', () => {
    const m = buildMapModel(multiNsReport(['a'], 60, 70))
    const c = collapseModel(m)
    const nodeGroup = c.nodes.find((n) => n.key === groupKey('Node'))!
    expect(nodeGroup).toMatchObject({ variant: 'group', kind: 'Node', namespace: '' })
    expect(nodeGroup.members).toHaveLength(70)
    expect(nodeGroup.key).not.toBe(groupKey('Node', 'a'))
    expect(groupKey('Node')).toBe(groupKey('Node', ''))
    expect(c.nodes.find((n) => n.key === groupKey('Pod', 'a'))!.members).toHaveLength(60)
  })

  it('раскрытие одной группы не раскрывает другую', () => {
    const c = collapseModel(ns2, new Set([groupKey('Pod', 'a')]))
    expect(c.nodes.filter((n) => n.kind === 'Pod' && n.variant === 'normal' && n.namespace === 'a')).toHaveLength(60)
    expect(c.nodes.some((n) => n.key === groupKey('Pod', 'a'))).toBe(false)
    expect(c.nodes.find((n) => n.key === groupKey('Pod', 'b'))!.members).toHaveLength(60)
    // раскрыть всё — граф возвращается к исходному числу вершин
    expect(collapseModel(ns2, new Set(groupKeysOf(ns2))).nodes).toHaveLength(ns2.nodes.length)
  })

  it('рёбра после сворачивания: по одному на группу, концы существуют, у раскрытой группы — исходные', () => {
    const c = collapseModel(ns2)
    const keys = new Set(c.nodes.map((n) => n.key))
    expect(c.edges).toHaveLength(2)
    expect(c.edges.map((e) => e.to).sort()).toEqual([groupKey('Pod', 'a'), groupKey('Pod', 'b')])
    for (const e of c.edges) expect(keys.has(e.from) && keys.has(e.to)).toBe(true)

    const open = collapseModel(ns2, new Set([groupKey('Pod', 'a')]))
    const openKeys = new Set(open.nodes.map((n) => n.key))
    expect(open.edges).toHaveLength(60 + 1)
    for (const e of open.edges) expect(openKeys.has(e.from) && openKeys.has(e.to)).toBe(true)
  })

  it('худшее состояние ребра побеждает при слиянии в группу', () => {
    const r = multiNsReport(['a'], 120) // 121 вершина — выше порога свёртки
    r.graph!.edges![5] = edge('apps/Deployment/a/web', 'core/Pod/a/p5', 'owns', 'broken')
    const c = collapseModel(buildMapModel(r))
    expect(c.edges).toHaveLength(1)
    expect(c.edges[0]).toMatchObject({ to: groupKey('Pod', 'a'), state: 'broken' })
  })

  it('ключи групп стабильны, однозначны и не зависят от порядка входа', () => {
    expect(groupKey('Pod', 'a')).toBe('group:ns/a/Pod')
    expect(groupKey('Node')).toBe('group:cluster/Node')
    expect(isGroupKey(groupKey('Pod', 'a'))).toBe(true)
    expect(isGroupKey('core/Pod/a/p1')).toBe(false)
    const keys = new Set([groupKey('Pod', 'a'), groupKey('Pod', 'b'), groupKey('Pod'), groupKey('Node'), groupKey('Node', 'a'), groupKey('a', 'Pod')])
    expect(keys.size).toBe(6)
    // разделитель в данных не даёт коллизий
    expect(groupKey('X', 'a/Y')).not.toBe(groupKey('Y', 'a/X'))
    expect(groupKey('Pod', 'a/b')).not.toBe(groupKey('b/Pod', 'a'))
    const r = multiNsReport(['a', 'b'], 60)
    const shuffled = { ...r, graph: { nodes: [...r.graph!.nodes].reverse(), edges: [...r.graph!.edges!].reverse() } }
    expect(collapseModel(buildMapModel(shuffled))).toEqual(collapseModel(buildMapModel(r)))
    expect(groupKeysOf(ns2)).toEqual([groupKey('Deployment', 'a'), groupKey('Pod', 'a'), groupKey('Pod', 'b')])
    expect(groupKeyOf({ kind: 'Pod', namespace: 'a' })).toBe(groupKey('Pod', 'a'))
  })

  it('группа меньше минимума не сворачивается', () => {
    const m = buildMapModel(multiNsReport(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z', 'aa', 'ab', 'ac', 'ad', 'ae', 'af', 'ag', 'ah', 'ai', 'aj', 'ak', 'al', 'am', 'an', 'ao', 'ap', 'aq', 'ar', 'as', 'at', 'au', 'av', 'aw', 'ax', 'ay', 'az'], 2))
    expect(m.nodes.length).toBeGreaterThan(MAP_COLLAPSE_THRESHOLD)
    expect(collapseModel(m)).toBe(m) // по 2 Pod в namespace — ниже MAP_COLLAPSE_MIN_GROUP
  })

  it('производительность: граф из 1000 вершин в 20 namespace сворачивается быстро', () => {
    const namespaces = Array.from({ length: 20 }, (_, i) => `ns${i}`)
    const m = buildMapModel(multiNsReport(namespaces, 49, 10))
    expect(m.nodes.length).toBe(991)
    const t0 = performance.now()
    const c = collapseModel(m)
    const ms = performance.now() - t0
    expect(c.nodes.filter((n) => n.variant === 'group')).toHaveLength(21) // 20 namespace × Pod + кластерные Node
    expect(c.nodes.length).toBeLessThan(MAP_COLLAPSE_THRESHOLD)
    expect(ms).toBeLessThan(1000)
  })
})

describe('связи: причина ребра', () => {
  /** Как отдаёт бэкенд: необязательный ConfigMap отсутствует — вершина Healthy с reason, ребро ok с reason optional_absent. */
  const optionalAbsent = (): Report => reportOf(
    [
      { kind: 'Pod', name: 'api-1', namespace: 'shop', group: 'core', health: 'Healthy' },
      { kind: 'ConfigMap', name: 'flags', namespace: 'shop', group: 'core', health: 'Healthy', reason: 'optional_absent' },
    ],
    [{ from: 'core/Pod/shop/api-1', to: 'core/ConfigMap/shop/flags', type: 'mounts', state: 'ok', reason: 'optional_absent' }],
  )

  it('reason ребра не теряется при переводе в связь; связь не становится broken/missing', () => {
    const m = buildMapModel(optionalAbsent())
    expect(m.edges[0]).toMatchObject({ state: 'ok', reason: 'optional_absent' })
    const pod = relationsOf(m, 'core/Pod/shop/api-1')
    expect(pod).toHaveLength(1)
    expect(pod[0]).toMatchObject({ direction: 'out', type: 'mounts', state: 'ok', reason: 'optional_absent' })
    const cm = relationsOf(m, 'core/ConfigMap/shop/flags')
    expect(cm[0]).toMatchObject({ direction: 'in', state: 'ok', reason: 'optional_absent' })
    const node = m.nodes.find((n) => n.kind === 'ConfigMap')!
    expect(node).toMatchObject({ health: 'Healthy', variant: 'normal', reason: 'optional_absent' })
  })

  it('связь повторяет reason своего ребра: есть у ребра — есть у связи, нет — нет', () => {
    const m = buildMapModel(rep(ingress))
    const web = find(m, 'Ingress', 'web').key
    const rels = relationsOf(m, web)
    expect(rels.some((r) => r.reason !== undefined)).toBe(true)
    for (const r of rels) {
      const e = m.edges.find((x) => x.type === r.type && x.from === web && x.to === r.other.key)!
      expect(r.reason).toBe(e.reason)
    }
    const plain = relationsOf(buildMapModel(optionalAbsent()), 'core/Pod/shop/api-1')
    expect(plain[0].reason).toBe('optional_absent')
  })
})
