// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Раскладка графа цепочки Flux «источник → объект Flux → ресурсы» (без Vue, Wails и i18n).
 * Слои задаются ролью узла, а не направлением рёбер sourceRef и dependsOn: контракт не фиксирует их направление.
 * Слой 0 — источники, слой 1 — объекты Flux, слои 2 и дальше — ресурсы по самому длинному пути между ними
 * (owns, selects, routes). Порядок внутри слоя — по среднему положению родителей, затем по виду, namespace и имени.
 */
import type { FluxGraph, FluxGraphEdge, FluxGraphEdgeType, FluxGraphNode, FluxGraphRole } from '../types/flux'

export const NODE_W = 190
export const NODE_H = 46
export const GAP_X = 70
export const GAP_Y = 14
/** Больше стольких одиночных ресурсов одного вида (ConfigMap, ClusterRole ...) схлопываются в одну вершину */
export const KIND_COLLAPSE_AT = 4
export const GROUP_PREFIX = 'group:'

/** Что показывает вкладка: нет данных, граф недоступен (Flux < 2.8), ресурсы в другом кластере или граф */
export type FluxGraphMode = 'none' | 'unavailable' | 'remote' | 'graph'

export function graphMode(g: FluxGraph | null | undefined): FluxGraphMode {
  if (!g) return 'none'
  if (!g.available) return 'unavailable'
  if (g.remote) return 'remote'
  return 'graph'
}

export type FluxTone = 'ok' | 'warn' | 'err' | 'unk'

/** Коды состояния узла: состояния Flux и health ресурсов Kubernetes; всё прочее — Unknown */
const NODE_STATES: ReadonlySet<string> = new Set([
  'Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled', 'Healthy', 'Degraded', 'Progressing', 'Missing', 'Unknown',
])

/** Код состояния для подписи: пустое и неизвестное значение — Unknown (подпись остаётся, не только цвет) */
export function nodeStateCode(n: Pick<FluxGraphNode, 'state'>): string {
  return n.state && NODE_STATES.has(n.state) ? n.state : 'Unknown'
}

export function nodeTone(n: Pick<FluxGraphNode, 'state'>): FluxTone {
  switch (nodeStateCode(n)) {
    case 'Ready': case 'Healthy': return 'ok'
    case 'Reconciling': case 'Suspended': case 'Progressing': return 'warn'
    case 'Failed': case 'Stalled': case 'Degraded': case 'Missing': return 'err'
    default: return 'unk'
  }
}

/** Более «тяжёлое» из двух состояний (для сводки схлопнутой группы) */
const SEVERITY: Record<string, number> = { Missing: 5, Degraded: 5, Stalled: 5, Failed: 5, Progressing: 4, Reconciling: 4, Suspended: 3, Unknown: 2, Healthy: 1, Ready: 1 }
export function worstState(a: string, b: string): string {
  return (SEVERITY[b] ?? 0) > (SEVERITY[a] ?? 0) ? b : a
}

export interface PlacedFluxNode {
  id: string
  kind: string
  name: string
  namespace: string
  role: FluxGraphRole
  state: string
  x: number
  y: number
  w: number
  h: number
  layer: number
  /** схлопнутая группа ресурсов одного вида: сколько их и их id */
  count: number
  members: string[]
}

export interface PlacedFluxEdge {
  id: string
  from: string
  to: string
  type: FluxGraphEdgeType
  d: string
}

export interface FluxGraphLayout {
  nodes: PlacedFluxNode[]
  edges: PlacedFluxEdge[]
  width: number
  height: number
}

const RESOURCE_EDGES: ReadonlySet<string> = new Set(['owns', 'selects', 'routes'])

const byIdentity = (a: { kind: string; namespace: string; name: string }, b: { kind: string; namespace: string; name: string }) =>
  a.kind.localeCompare(b.kind) || a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name)

interface Item extends FluxGraphNode { count: number; members: string[] }

/** Рёбра без дублей и без ссылок на отсутствующие узлы (частичный граф при Truncated не ломает раскладку) */
export function cleanEdges(nodes: readonly { id: string }[], edges: readonly FluxGraphEdge[] | null | undefined): FluxGraphEdge[] {
  const ids = new Set(nodes.map(n => n.id))
  const seen = new Set<string>()
  const out: FluxGraphEdge[] = []
  for (const e of edges ?? []) {
    if (!e || !ids.has(e.from) || !ids.has(e.to) || e.from === e.to) continue
    const key = `${e.from}|${e.to}|${e.type}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(e)
  }
  return out
}

/**
 * Схлопывает одиночные ресурсы одного вида, если их больше KIND_COLLAPSE_AT: у них нет связей с другими
 * ресурсами, поэтому по отдельности они ничего не добавляют. Рабочие нагрузки со связями остаются узлами.
 * Группы из expanded остаются развёрнутыми. Идентификатор группы — `group:<Kind>`.
 */
export function collapseKinds(
  nodes: readonly FluxGraphNode[],
  edges: readonly FluxGraphEdge[],
  expanded: ReadonlySet<string> = new Set(),
): { nodes: Item[]; edges: FluxGraphEdge[] } {
  const linked = new Set<string>()
  for (const e of edges) if (RESOURCE_EDGES.has(e.type)) { linked.add(e.from); linked.add(e.to) }

  const loners = new Map<string, FluxGraphNode[]>()
  for (const n of nodes) {
    if (n.role !== 'resource' || linked.has(n.id)) continue
    const list = loners.get(n.kind)
    if (list) list.push(n)
    else loners.set(n.kind, [n])
  }

  const replaced = new Map<string, string>()
  const groups: Item[] = []
  for (const [kind, list] of loners) {
    const id = GROUP_PREFIX + kind
    if (list.length <= KIND_COLLAPSE_AT || expanded.has(id)) continue
    let state = ''
    for (const n of list) { replaced.set(n.id, id); state = worstState(state, n.state) }
    groups.push({
      id, kind, group: '', version: '', namespace: '', name: '', role: 'resource', state, count: list.length, members: list.map(n => n.id),
    })
  }

  const kept: Item[] = nodes.filter(n => !replaced.has(n.id)).map(n => ({ ...n, count: 1, members: [n.id] }))
  const seen = new Set<string>()
  const mapped: FluxGraphEdge[] = []
  for (const e of edges) {
    const f = replaced.get(e.from) ?? e.from
    const t = replaced.get(e.to) ?? e.to
    const key = `${f}|${t}|${e.type}`
    if (seen.has(key)) continue
    seen.add(key)
    mapped.push({ from: f, to: t, type: e.type })
  }
  return { nodes: [...kept, ...groups], edges: mapped }
}

/** Слой узла: источники 0, объекты Flux 1, ресурсы от 2 по самому длинному пути между ресурсами */
function layers(nodes: readonly Item[], edges: readonly FluxGraphEdge[]): Map<string, number> {
  const layer = new Map<string, number>()
  for (const n of nodes) layer.set(n.id, n.role === 'source' ? 0 : n.role === 'flux' ? 1 : 2)
  const isResource = new Map(nodes.map(n => [n.id, n.role === 'resource']))
  // не более числа узлов проходов: цикл в данных не зациклит раскладку
  for (let pass = 0; pass < nodes.length; pass++) {
    let moved = false
    for (const e of edges) {
      if (!RESOURCE_EDGES.has(e.type) || !isResource.get(e.from) || !isResource.get(e.to)) continue
      const want = (layer.get(e.from) ?? 2) + 1
      if (want > (layer.get(e.to) ?? 2)) { layer.set(e.to, want); moved = true }
    }
    if (!moved) break
  }
  return layer
}

/** Раскладка слоями слева направо; пустой граф даёт пустую раскладку */
export function layoutFluxGraph(g: FluxGraph | null | undefined, expanded: ReadonlySet<string> = new Set()): FluxGraphLayout {
  const baseNodes = (g?.nodes ?? []).filter(n => !!n?.id)
  const baseEdges = cleanEdges(baseNodes, g?.edges)
  const { nodes, edges } = collapseKinds(baseNodes, baseEdges, expanded)
  if (!nodes.length) return { nodes: [], edges: [], width: 0, height: 0 }

  const layer = layers(nodes, edges)
  const parents = new Map<string, string[]>()
  for (const e of edges) {
    const list = parents.get(e.to)
    if (list) list.push(e.from)
    else parents.set(e.to, [e.from])
  }

  const maxLayer = Math.max(...layer.values())
  const columns: Item[][] = Array.from({ length: maxLayer + 1 }, () => [])
  for (const n of nodes) columns[layer.get(n.id) ?? 2].push(n)

  const row = new Map<string, number>()
  const placed = new Map<string, PlacedFluxNode>()
  columns.forEach((col, ci) => {
    // порядок по среднему положению родителей из предыдущих слоёв, затем по виду, namespace и имени
    const score = (n: Item) => {
      const ps = (parents.get(n.id) ?? []).filter(p => (layer.get(p) ?? 0) < ci && row.has(p))
      return ps.length ? ps.reduce((s, p) => s + (row.get(p) as number), 0) / ps.length : Number.POSITIVE_INFINITY
    }
    col.sort((a, b) => {
      const sa = score(a); const sb = score(b)
      if (sa !== sb) return sa === Number.POSITIVE_INFINITY ? 1 : sb === Number.POSITIVE_INFINITY ? -1 : sa - sb
      return byIdentity(a, b)
    })
    col.forEach((n, ri) => {
      row.set(n.id, ri)
      placed.set(n.id, {
        id: n.id, kind: n.kind, name: n.name, namespace: n.namespace, role: n.role, state: n.state,
        x: ci * (NODE_W + GAP_X), y: ri * (NODE_H + GAP_Y), w: NODE_W, h: NODE_H, layer: ci, count: n.count, members: n.members,
      })
    })
  })

  const placedEdges: PlacedFluxEdge[] = edges.map(e => {
    const a = placed.get(e.from) as PlacedFluxNode
    const b = placed.get(e.to) as PlacedFluxNode
    return { id: `${e.from}|${e.to}|${e.type}`, from: e.from, to: e.to, type: e.type, d: edgePath(a, b) }
  })

  const all = [...placed.values()]
  return {
    nodes: all,
    edges: placedEdges,
    width: Math.max(...all.map(n => n.x + n.w)),
    height: Math.max(...all.map(n => n.y + n.h)),
  }
}

/** Кривая из правого края левого узла в левый край правого; узлы в одном слое соединяются дугой справа */
export function edgePath(a: PlacedFluxNode, b: PlacedFluxNode): string {
  if (a.layer === b.layer) {
    const x = a.x + a.w
    const y1 = a.y + a.h / 2
    const y2 = b.y + b.h / 2
    return `M${x},${y1} C${x + 40},${y1} ${x + 40},${y2} ${x},${y2}`
  }
  const [l, r] = a.layer < b.layer ? [a, b] : [b, a]
  const x1 = l.x + l.w
  const y1 = l.y + l.h / 2
  const x2 = r.x
  const y2 = r.y + r.h / 2
  const mid = (x1 + x2) / 2
  return `M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`
}
