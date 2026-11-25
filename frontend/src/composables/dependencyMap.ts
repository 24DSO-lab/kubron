// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Модель интерактивной карты зависимостей (ADR-0031 §8): чистые функции без DOM и i18n.
 * Вход — `Report.graph` отчёта (internal/depgraph.Snapshot), выход — узлы, рёбра и производные
 * множества (кандидаты, цепочки, фильтры, свёртка). Работает с ЛЮБЫМ графом: неизвестный вид
 * ресурса, состояние или тип ребра не роняют карту, а превращаются в нейтральные значения.
 * Имена ресурсов — только данные; ничего не логируется.
 */
import type { Fact, GraphEdge, GraphNode, Hypothesis, MissingContext, Report, ResourceRef } from '../types/incident'

// Лимиты и пороги.
/** Бэкенд отдаёт не более 200 вершин и 600 рёбер (ADR-0031 §3); карта рассчитана с запасом — на 1 000 вершин. */
export const MAP_NODE_LIMIT = 1000
/** Свёртка включается выше этого числа вершин: здоровые вершины одного вида сливаются в группу (ADR-0031 §3, §8). */
export const MAP_COLLAPSE_THRESHOLD = 100
/** Минимум здоровых вершин вида, при котором их имеет смысл сворачивать в группу. */
export const MAP_COLLAPSE_MIN_GROUP = 3

/** Известные состояния вершины (словарь depgraph.Health); всё остальное — 'other'. */
export const MAP_HEALTHS = ['Healthy', 'Degraded', 'Progressing', 'Suspended', 'Missing', 'Unknown'] as const
export type KnownHealth = (typeof MAP_HEALTHS)[number]
export type MapHealth = KnownHealth | 'other'

/** Известные причины Unknown (depgraph.UnknownReason); остальные — 'other'. */
export const MAP_UNKNOWN_REASONS = ['forbidden', 'unavailable', 'too_large', 'limit', 'policy'] as const
export type MapUnknownReason = (typeof MAP_UNKNOWN_REASONS)[number] | 'other'

/** Типы рёбер (depgraph.EdgeType); неизвестный тип рисуется нейтральным ребром 'other'. */
export const MAP_EDGE_TYPES = ['owns', 'selects', 'backed_by', 'routes', 'mounts', 'binds', 'runs_on', 'scales'] as const
export type MapEdgeType = (typeof MAP_EDGE_TYPES)[number] | 'other'
/** Рёбра, по которым строятся слои раскладки (ADR-0031 §8): владение, маршрут, масштабирование, выбор. */
export const HIERARCHY_EDGE_TYPES: ReadonlySet<string> = new Set(['owns', 'routes', 'scales', 'selects'])

export const MAP_EDGE_STATES = ['ok', 'broken', 'missing', 'unknown'] as const
export type MapEdgeState = (typeof MAP_EDGE_STATES)[number] | 'other'

/**
 * Вид вершины для рисования. Различимость не только цветом: у каждого варианта своя форма,
 * иконка и подпись (доступность).
 *  - normal      — обычная вершина;
 *  - placeholder — заглушка `Missing`: ресурса в кластере нет (NotFound);
 *  - unknown     — прочитать не удалось (причина в `unknown`);
 *  - secret      — Secret, `Unknown/policy`: не читается никогда;
 *  - group       — свёрнутая группа здоровых вершин одного вида.
 */
export type NodeVariant = 'normal' | 'placeholder' | 'unknown' | 'secret' | 'group'

export interface MapNode {
  key: string
  group: string
  kind: string
  namespace: string
  name: string
  health: MapHealth
  /** Исходное значение состояния — только для служебных нужд, в интерфейсе не показывается. */
  rawHealth: string
  reason?: string
  unknown?: MapUnknownReason
  variant: NodeVariant
  /** Вид из встроенного списка известных; неизвестный — нейтральная вершина. */
  knownKind: boolean
  candidate: boolean
  target: boolean
  /** Для группы: ключи свёрнутых вершин (отсортированы). */
  members?: string[]
}

export interface MapEdge {
  key: string
  from: string
  to: string
  type: MapEdgeType
  state: MapEdgeState
  reason?: string
  hierarchy: boolean
}

export interface MapModel {
  nodes: MapNode[]
  edges: MapEdge[]
  /** Нет графа (старый отчёт) или он пуст — карта скрывается без ошибки. */
  empty: boolean
  /** «Граф неполный»: усечён, есть предупреждения или отчёт частичный по графу. */
  incomplete: boolean
  /** Рёбра, чьи концы отсутствуют среди вершин, отброшены (не роняют карту). */
  droppedEdges: number
  targetKey: string
  candidateKeys: string[]
  /** Виды и состояния, реально встречающиеся в графе (для фильтров), отсортированы. */
  kinds: string[]
  healths: MapHealth[]
}

/** Иконки состояний: вместе с формой и подписью делают состояние различимым не только цветом (доступность). */
export const HEALTH_ICONS: Record<MapHealth, string> = { Healthy: '✓', Degraded: '!', Progressing: '…', Suspended: '⏸', Missing: '✗', Unknown: '?', other: '·' }
/** Иконки особых вершин: Secret (не читается намеренно) и свёрнутая группа. */
export const VARIANT_ICONS: Record<NodeVariant, string> = { normal: '', placeholder: '✗', unknown: '?', secret: '⊘', group: '≡' }

/** Имя для подписи на вершине: длинное сокращается, полное остаётся в `<title>` и деталях. */
export function shortName(name: string, max = 26): string {
  return name.length <= max ? name : `${name.slice(0, max - 1)}…`
}

/** Ключ вершины: как depgraph.Ref.Key() — «group/kind/namespace/name», пустая группа = core. */
export function nodeKey(group: string | undefined, kind: string, namespace: string | undefined, name: string): string {
  return `${group || 'core'}/${kind}/${namespace ?? ''}/${name}`
}

export function refKey(ref: ResourceRef): string {
  return nodeKey(ref.group, ref.kind, ref.namespace, ref.name)
}

const KNOWN_KINDS: ReadonlySet<string> = new Set([
  'Pod', 'Node', 'Service', 'PersistentVolumeClaim', 'PersistentVolume', 'ConfigMap', 'Secret', 'Deployment', 'ReplicaSet',
  'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'StorageClass', 'EndpointSlice', 'Ingress', 'IngressClass', 'HorizontalPodAutoscaler',
])

const HEALTH_RANK: Record<MapHealth, number> = { Healthy: 0, Suspended: 1, Progressing: 2, other: 3, Unknown: 4, Degraded: 5, Missing: 6 }

/** Худшее из состояний — для свёрнутой группы. */
export function worstHealth(list: MapHealth[]): MapHealth {
  return list.reduce<MapHealth>((acc, h) => (HEALTH_RANK[h] > HEALTH_RANK[acc] ? h : acc), 'Healthy')
}

function normHealth(h: unknown): MapHealth {
  return (MAP_HEALTHS as readonly string[]).includes(h as string) ? (h as KnownHealth) : 'other'
}

function normUnknown(u: unknown): MapUnknownReason | undefined {
  if (typeof u !== 'string' || !u) return undefined
  return (MAP_UNKNOWN_REASONS as readonly string[]).includes(u) ? (u as MapUnknownReason) : 'other'
}

function normEdgeType(t: unknown): MapEdgeType {
  return (MAP_EDGE_TYPES as readonly string[]).includes(t as string) ? (t as MapEdgeType) : 'other'
}

function normEdgeState(s: unknown): MapEdgeState {
  return (MAP_EDGE_STATES as readonly string[]).includes(s as string) ? (s as MapEdgeState) : 'other'
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '')

function variantOf(n: { kind: string; health: MapHealth; unknown?: MapUnknownReason; placeholder?: boolean }): NodeVariant {
  // Secret — всегда Unknown/policy; различаем его от «не смогли прочитать» формой и подписью.
  if (n.kind === 'Secret' && n.health === 'Unknown') return 'secret'
  if (n.health === 'Missing' || n.placeholder) return 'placeholder'
  if (n.health === 'Unknown') return 'unknown'
  return 'normal'
}

const compareKeys = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)

function toMapNode(n: GraphNode): MapNode | null {
  const kind = str(n?.kind)
  const name = str(n?.name)
  if (!kind || !name) return null
  const group = str(n.group) || 'core'
  const health = normHealth(n.health)
  const unknown = health === 'Unknown' ? (normUnknown(n.unknown) ?? 'other') : undefined
  return {
    key: nodeKey(group, kind, n.namespace, name),
    group, kind, namespace: str(n.namespace), name,
    health, rawHealth: str(n.health),
    reason: str(n.reason) || undefined,
    unknown,
    variant: variantOf({ kind, health, unknown, placeholder: n.placeholder === true }),
    knownKind: KNOWN_KINDS.has(kind),
    candidate: false,
    target: false,
  }
}

function toMapEdge(e: GraphEdge): MapEdge | null {
  const from = str(e?.from)
  const to = str(e?.to)
  if (!from || !to) return null
  const type = normEdgeType(e.type)
  return {
    key: `${from}|${str(e.type)}|${to}`,
    from, to, type,
    state: normEdgeState(e.state),
    reason: str(e.reason) || undefined,
    hierarchy: HIERARCHY_EDGE_TYPES.has(type),
  }
}

/** Ключи кандидатов первопричины: гипотезы, указанные цепочками, и гипотезы с непустым `explains`. */
export function candidateKeysOf(report: Report): string[] {
  const hyps: Hypothesis[] = report.hypotheses ?? []
  const byId = new Map(hyps.map((h) => [h.id, h]))
  const keys = new Set<string>()
  for (const c of report.chains ?? []) {
    const h = c?.candidate ? byId.get(c.candidate) : undefined
    if (h?.resource) keys.add(refKey(h.resource))
  }
  for (const h of hyps) if (h.explains?.length && h.resource) keys.add(refKey(h.resource))
  return [...keys].sort(compareKeys)
}

/** «Граф неполный»: усечение, предупреждения построителя или пометка отчёта по графу. */
function isIncomplete(report: Report): boolean {
  const g = report.graph as (Report['graph'] & { partial?: boolean }) | undefined
  if (!g) return false
  if (g.truncated === true || g.partial === true) return true
  if ((g.warnings?.length ?? 0) > 0) return true
  // Бэкенд пишет в Partial отчёта код «Graph» (diagnose/graph_hook.go).
  return (report.partial ?? []).some((p) => typeof p === 'string' && p.toLowerCase().startsWith('graph'))
}

/**
 * Приводит `Report.graph` к модели карты. Не бросает исключений: null-срезы Go, дубликаты, рёбра
 * без вершин, неизвестные виды и состояния обрабатываются штатно. Порядок вершин и рёбер
 * детерминирован (сортировка по ключу), поэтому одинаковый отчёт даёт одинаковую карту.
 */
export function buildMapModel(report: Report | null | undefined): MapModel {
  const empty: MapModel = {
    nodes: [], edges: [], empty: true, incomplete: false, droppedEdges: 0, targetKey: '', candidateKeys: [], kinds: [], healths: [],
  }
  const graph = report?.graph
  if (!report || !graph) return empty

  const byKey = new Map<string, MapNode>()
  for (const raw of (graph.nodes ?? []).slice(0, MAP_NODE_LIMIT)) {
    const n = toMapNode(raw)
    // Повтор ключа: побеждает более «тяжёлое» состояние — карта не прячет проблему за дублем.
    if (n && (!byKey.has(n.key) || HEALTH_RANK[n.health] > HEALTH_RANK[byKey.get(n.key)!.health])) byKey.set(n.key, n)
  }
  if (byKey.size === 0) return { ...empty, incomplete: isIncomplete(report) }

  const edgeMap = new Map<string, MapEdge>()
  let dropped = 0
  for (const raw of graph.edges ?? []) {
    const e = toMapEdge(raw)
    if (!e || !byKey.has(e.from) || !byKey.has(e.to) || e.from === e.to) {
      dropped++
      continue
    }
    if (!edgeMap.has(e.key)) edgeMap.set(e.key, e)
  }

  const candidateKeys = candidateKeysOf(report).filter((k) => byKey.has(k))
  const targetKey = report.target ? refKey(report.target) : ''
  for (const k of candidateKeys) byKey.get(k)!.candidate = true
  if (byKey.has(targetKey)) byKey.get(targetKey)!.target = true

  const nodes = [...byKey.values()].sort((a, b) => compareKeys(a.key, b.key))
  const edges = [...edgeMap.values()].sort((a, b) => compareKeys(a.key, b.key))
  return {
    nodes, edges, empty: false, incomplete: isIncomplete(report), droppedEdges: dropped,
    targetKey: byKey.has(targetKey) ? targetKey : '',
    candidateKeys,
    kinds: [...new Set(nodes.map((n) => n.kind))].sort(compareKeys),
    healths: [...new Set(nodes.map((n) => n.health))].sort((a, b) => HEALTH_RANK[b] - HEALTH_RANK[a]),
  }
}

// ---- Цепочки ----

export interface Highlight {
  nodes: ReadonlySet<string>
  edges: ReadonlySet<string>
}

const NO_HIGHLIGHT: Highlight = { nodes: new Set(), edges: new Set() }

/** Кратчайший путь (по числу рёбер) от `from` до `to`: сначала по направлению рёбер, затем без учёта направления. */
export function shortestPath(model: MapModel, from: string, to: string): string[] {
  if (from === to) return [from]
  for (const directed of [true, false]) {
    const adj = new Map<string, string[]>()
    for (const e of model.edges) {
      if (!adj.has(e.from)) adj.set(e.from, [])
      adj.get(e.from)!.push(e.to)
      if (!directed) {
        if (!adj.has(e.to)) adj.set(e.to, [])
        adj.get(e.to)!.push(e.from)
      }
    }
    const prev = new Map<string, string>([[from, '']])
    const queue = [from]
    for (let i = 0; i < queue.length; i++) {
      const cur = queue[i]
      for (const next of [...(adj.get(cur) ?? [])].sort(compareKeys)) {
        if (prev.has(next)) continue
        prev.set(next, cur)
        if (next === to) {
          const path = [to]
          while (path[0] !== from) path.unshift(prev.get(path[0])!)
          return path
        }
        queue.push(next)
      }
    }
  }
  return []
}

/**
 * Подсветка цепочки до кандидата первопричины. Источник — `Report.chains` (путь от затронутого
 * ресурса); если цепочек нет, путь от цели до каждого кандидата ищется по рёбрам графа.
 * Ресурсов, которых нет на карте, касается только пропуск шага.
 */
export function highlightChains(model: MapModel, report: Report | null | undefined): Highlight {
  if (model.empty || !report) return NO_HIGHLIGHT
  const present = new Set(model.nodes.map((n) => n.key))
  const nodes = new Set<string>()
  const edges = new Set<string>()
  const addPath = (keys: string[]) => {
    for (const k of keys) nodes.add(k)
    for (let i = 0; i + 1 < keys.length; i++) {
      for (const e of model.edges) {
        if ((e.from === keys[i] && e.to === keys[i + 1]) || (e.from === keys[i + 1] && e.to === keys[i])) edges.add(e.key)
      }
    }
  }
  for (const c of report.chains ?? []) {
    const keys = (c?.path ?? []).map((s) => refKey(s.ref)).filter((k) => present.has(k))
    if (keys.length) addPath(keys)
  }
  if (nodes.size === 0 && model.targetKey) {
    for (const cand of model.candidateKeys) addPath(shortestPath(model, model.targetKey, cand))
  }
  return { nodes, edges }
}

// ---- Фильтры ----

export interface MapFilter {
  /** Показываемые виды; null — все. */
  kinds: ReadonlySet<string> | null
  /** Показываемые состояния; null — все. */
  healths: ReadonlySet<MapHealth> | null
}

export const NO_FILTER: MapFilter = { kinds: null, healths: null }

export function isFilterActive(f: MapFilter): boolean {
  return f.kinds !== null || f.healths !== null
}

/**
 * Применяет фильтры по виду и состоянию. Цель, кандидаты и вершины из `keep` (цепочка, выбранная)
 * не скрываются никогда: фильтр не должен прятать то, ради чего открыта карта. Рёбра со скрытым концом скрываются.
 */
export function filterModel(model: MapModel, filter: MapFilter, keep: ReadonlySet<string> = new Set()): MapModel {
  if (!isFilterActive(filter)) return model
  const show = (n: MapNode) =>
    n.target || n.candidate || keep.has(n.key) ||
    ((filter.kinds === null || filter.kinds.has(n.kind)) && (filter.healths === null || filter.healths.has(n.health)))
  const nodes = model.nodes.filter(show)
  const visible = new Set(nodes.map((n) => n.key))
  return { ...model, nodes, edges: model.edges.filter((e) => visible.has(e.from) && visible.has(e.to)) }
}

// ---- Свёртка больших графов ----

export const groupKey = (kind: string) => `group:${kind}`
export const isGroupKey = (key: string) => key.startsWith('group:')

/**
 * Большой граф (> `threshold` вершин) свёртывается по виду: здоровые, не выбранные и не лежащие на
 * цепочке вершины одного вида сливаются в группу «N × Вид»; раскрытие — через `expandedKinds`.
 * Проблемные вершины (любое состояние кроме Healthy), цель, кандидаты и `keep` остаются
 * отдельными. Рёбра переводятся на группы и дедуплицируются; худшее состояние побеждает.
 */
export function collapseModel(
  model: MapModel,
  expandedKinds: ReadonlySet<string> = new Set(),
  keep: ReadonlySet<string> = new Set(),
  threshold: number = MAP_COLLAPSE_THRESHOLD,
): MapModel {
  if (model.nodes.length <= threshold) return model
  const collapsible = (n: MapNode) =>
    n.health === 'Healthy' && n.variant === 'normal' && !n.target && !n.candidate && !keep.has(n.key) && !expandedKinds.has(n.kind)
  const byKind = new Map<string, MapNode[]>()
  for (const n of model.nodes) if (collapsible(n)) byKind.set(n.kind, [...(byKind.get(n.kind) ?? []), n])

  const redirect = new Map<string, string>()
  const groups: MapNode[] = []
  for (const [kind, members] of [...byKind.entries()].sort((a, b) => compareKeys(a[0], b[0]))) {
    if (members.length < MAP_COLLAPSE_MIN_GROUP) continue
    const key = groupKey(kind)
    for (const m of members) redirect.set(m.key, key)
    groups.push({
      key, group: '', kind, namespace: '', name: String(members.length), health: 'Healthy', rawHealth: 'Healthy',
      variant: 'group', knownKind: KNOWN_KINDS.has(kind), candidate: false, target: false,
      members: members.map((m) => m.key).sort(compareKeys),
    })
  }
  if (groups.length === 0) return model

  const nodes = [...model.nodes.filter((n) => !redirect.has(n.key)), ...groups].sort((a, b) => compareKeys(a.key, b.key))
  const merged = new Map<string, MapEdge>()
  for (const e of model.edges) {
    const from = redirect.get(e.from) ?? e.from
    const to = redirect.get(e.to) ?? e.to
    if (from === to) continue
    const key = `${from}|${e.type}|${to}`
    const prev = merged.get(key)
    // Худшее состояние ребра побеждает: свёртка не должна скрывать оборванную связь.
    const rank = (s: MapEdgeState) => ({ ok: 0, other: 1, unknown: 2, broken: 3, missing: 4 })[s]
    if (!prev || rank(e.state) > rank(prev.state)) merged.set(key, { ...e, key, from, to })
  }
  return { ...model, nodes, edges: [...merged.values()].sort((a, b) => compareKeys(a.key, b.key)) }
}

// ---- Находки по вершине ----

export interface NodeFindings {
  facts: Fact[]
  hypotheses: Hypothesis[]
  missing: MissingContext[]
}

const EMPTY_FINDINGS: NodeFindings = { facts: [], hypotheses: [], missing: [] }

/** Факты, гипотезы и недостающий контекст отчёта по ресурсу вершины. Для группы — пусто. */
export function findingsFor(report: Report | null | undefined, key: string): NodeFindings {
  if (!report || !key || isGroupKey(key)) return EMPTY_FINDINGS
  return {
    facts: (report.facts ?? []).filter((f) => f?.resource && refKey(f.resource) === key),
    hypotheses: (report.hypotheses ?? []).filter((h) => h?.resource && refKey(h.resource) === key),
    missing: (report.missing ?? []).filter((m) => m?.resource && refKey(m.resource) === key),
  }
}

export interface NodeRelation {
  direction: 'in' | 'out'
  type: MapEdgeType
  state: MapEdgeState
  other: MapNode
}

/** Связи вершины: исходящие и входящие рёбра с соседом; порядок детерминирован. */
export function relationsOf(model: MapModel, key: string): NodeRelation[] {
  const byKey = new Map(model.nodes.map((n) => [n.key, n]))
  const out: NodeRelation[] = []
  for (const e of model.edges) {
    if (e.from === key && byKey.has(e.to)) out.push({ direction: 'out', type: e.type, state: e.state, other: byKey.get(e.to)! })
    else if (e.to === key && byKey.has(e.from)) out.push({ direction: 'in', type: e.type, state: e.state, other: byKey.get(e.from)! })
  }
  return out.sort((a, b) => compareKeys(`${a.direction}|${a.type}|${a.other.key}`, `${b.direction}|${b.type}|${b.other.key}`))
}

// ---- Клавиатура ----

export type NavKey = 'ArrowUp' | 'ArrowDown' | 'ArrowLeft' | 'ArrowRight' | 'Home' | 'End'
export interface Point { x: number; y: number }

export function isNavKey(key: string): key is NavKey {
  return key === 'ArrowUp' || key === 'ArrowDown' || key === 'ArrowLeft' || key === 'ArrowRight' || key === 'Home' || key === 'End'
}

/**
 * Следующая вершина при нажатии стрелки по раскладке: влево/вправо — ближайшая вершина в соседнем
 * слое (меньше смещение по вертикали), вверх/вниз — соседняя вершина того же слоя. `Home`/`End` —
 * первая и последняя. Без текущей вершины стрелки выбирают первую. Нет соседа — остаёмся на месте.
 */
export function navigate(positions: Readonly<Record<string, Point>>, current: string | null, key: NavKey): string | null {
  const keys = Object.keys(positions).sort((a, b) => positions[a].x - positions[b].x || positions[a].y - positions[b].y || compareKeys(a, b))
  if (keys.length === 0) return null
  if (key === 'Home') return keys[0]
  if (key === 'End') return keys[keys.length - 1]
  if (!current || !positions[current]) return keys[0]
  const cur = positions[current]
  const EPS = 1
  let best: string | null = null
  let bestScore = Infinity
  for (const k of keys) {
    if (k === current) continue
    const p = positions[k]
    let score = -1
    if (key === 'ArrowRight' && p.x > cur.x + EPS) score = (p.x - cur.x) * 4 + Math.abs(p.y - cur.y)
    else if (key === 'ArrowLeft' && p.x < cur.x - EPS) score = (cur.x - p.x) * 4 + Math.abs(p.y - cur.y)
    else if (key === 'ArrowDown' && Math.abs(p.x - cur.x) <= EPS && p.y > cur.y + EPS) score = p.y - cur.y
    else if (key === 'ArrowUp' && Math.abs(p.x - cur.x) <= EPS && p.y < cur.y - EPS) score = cur.y - p.y
    if (score >= 0 && score < bestScore) {
      best = k
      bestScore = score
    }
  }
  return best ?? current
}
