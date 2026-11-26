// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Раскладка карты зависимостей (ADR-0031 §8): детерминированная слоистая, без внешних библиотек.
 *
 * Слои задают рёбра иерархии (`owns`, `routes`, `scales`, `selects`): родитель всегда левее
 * потомка. Остальные рёбра (`mounts`, `binds`, `backed_by`, `runs_on`, неизвестные) слои не
 * меняют, а лишь размещают вершины, до которых иерархия не дотянулась, на слой правее источника.
 * Порядок внутри слоя — барицентрическая эвристика с фиксированным числом проходов; все
 * равенства разрешаются сравнением ключей, поэтому результат не зависит от порядка входных данных.
 */
import type { MapEdge, MapNode, Point } from './dependencyMap'

/** Размеры вершины и шаги сетки (единицы SVG). */
export const NODE_W = 184
export const NODE_H = 58
export const COL_GAP = 84
export const ROW_GAP = 22
export const PADDING = 32
/** Число проходов барицентрической сортировки (ADR-0031 §8: достаточно для читаемости, дёшево на 1 000 вершин). */
const SWEEPS = 4

export interface MapLayout {
  positions: Record<string, Point>
  layers: Record<string, number>
  width: number
  height: number
  layerCount: number
}

const compareKeys = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)

/** Слои по рёбрам иерархии: самый длинный путь от корня; вершины на циклах получают слой по уже размеченным предкам. */
function hierarchyLayers(keys: string[], edges: MapEdge[]): Map<string, number> {
  const layer = new Map<string, number>()
  const hier = edges.filter((e) => e.hierarchy)
  const touched = new Set<string>()
  const indeg = new Map<string, number>()
  const out = new Map<string, string[]>()
  for (const e of hier) {
    touched.add(e.from)
    touched.add(e.to)
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1)
    if (!out.has(e.from)) out.set(e.from, [])
    out.get(e.from)!.push(e.to)
  }
  const sortedTouched = keys.filter((k) => touched.has(k))
  let ready = sortedTouched.filter((k) => !indeg.get(k)).sort(compareKeys)
  for (const k of ready) layer.set(k, 0)
  const remaining = new Map(indeg)
  while (ready.length) {
    const next: string[] = []
    for (const k of ready) {
      for (const to of (out.get(k) ?? []).sort(compareKeys)) {
        layer.set(to, Math.max(layer.get(to) ?? 0, (layer.get(k) ?? 0) + 1))
        remaining.set(to, (remaining.get(to) ?? 1) - 1)
        if (remaining.get(to) === 0) next.push(to)
      }
    }
    ready = [...new Set(next)].sort(compareKeys)
  }
  // Цикл и всё, что за ним: проходами по размеченным предкам; если таких нет, первая по ключу вершина
  // становится корнем цикла (слой 0). Каждый проход размечает хотя бы одну вершину — завершение гарантировано.
  let pending = sortedTouched.filter((k) => !layer.has(k))
  while (pending.length) {
    const assigned = new Map<string, number>()
    for (const k of pending) {
      let l = -1
      for (const e of hier) if (e.to === k && layer.has(e.from)) l = Math.max(l, (layer.get(e.from) ?? 0) + 1)
      if (l >= 0) assigned.set(k, l)
    }
    if (assigned.size === 0) assigned.set(pending[0], 0)
    for (const [k, l] of assigned) layer.set(k, l)
    pending = pending.filter((k) => !layer.has(k))
  }
  return layer
}

/** Вершины, до которых иерархия не дотянулась, ставятся по прочим рёбрам: правее источника или левее цели. */
function placeRest(keys: string[], edges: MapEdge[], layer: Map<string, number>): void {
  const rest = edges.filter((e) => !e.hierarchy)
  // Проход вперёд: слой цели = максимум по размеченным источникам + 1. Число итераций ограничено числом вершин.
  for (let i = 0; i < keys.length; i++) {
    let changed = false
    for (const k of keys) {
      if (layer.has(k)) continue
      let l = -1
      for (const e of rest) if (e.to === k && layer.has(e.from)) l = Math.max(l, (layer.get(e.from) ?? 0) + 1)
      if (l >= 0) {
        layer.set(k, l)
        changed = true
      }
    }
    if (!changed) break
  }
  // Проход назад: вершина-источник без слоя ставится левее самой левой размеченной цели.
  for (let i = 0; i < keys.length; i++) {
    let changed = false
    for (const k of keys) {
      if (layer.has(k)) continue
      let l = Infinity
      for (const e of rest) if (e.from === k && layer.has(e.to)) l = Math.min(l, (layer.get(e.to) ?? 0) - 1)
      if (l !== Infinity) {
        layer.set(k, Math.max(0, l))
        changed = true
      }
    }
    if (!changed) break
  }
  for (const k of keys) if (!layer.has(k)) layer.set(k, 0)
}

/** Порядок внутри слоёв: барицентры соседей из соседних слоёв, ничья — по ключу. */
function orderLayers(layers: string[][], edges: MapEdge[], layerOf: Map<string, number>): void {
  const neighbours = new Map<string, string[]>()
  for (const e of edges) {
    if (!neighbours.has(e.from)) neighbours.set(e.from, [])
    if (!neighbours.has(e.to)) neighbours.set(e.to, [])
    neighbours.get(e.from)!.push(e.to)
    neighbours.get(e.to)!.push(e.from)
  }
  const index = new Map<string, number>()
  const reindex = () => layers.forEach((l) => l.forEach((k, i) => index.set(k, i)))
  layers.forEach((l) => l.sort(compareKeys))
  reindex()
  const sweep = (from: number, to: number, step: number) => {
    for (let li = from; li !== to; li += step) {
      const ref = li - step
      const bary = new Map<string, number>()
      for (const k of layers[li]) {
        const ns = (neighbours.get(k) ?? []).filter((n) => layerOf.get(n) === ref)
        bary.set(k, ns.length ? ns.reduce((s, n) => s + (index.get(n) ?? 0), 0) / ns.length : (index.get(k) ?? 0))
      }
      layers[li].sort((a, b) => bary.get(a)! - bary.get(b)! || compareKeys(a, b))
      layers[li].forEach((k, i) => index.set(k, i))
    }
  }
  for (let s = 0; s < SWEEPS; s++) {
    if (layers.length > 1) {
      sweep(1, layers.length, 1)
      sweep(layers.length - 2, -1, -1)
    }
  }
}

/** Раскладка вершин: слои — столбцы слева направо, внутри слоя — сверху вниз, столбцы центрируются по высоте. */
export function layoutMap(nodes: readonly MapNode[], edges: readonly MapEdge[]): MapLayout {
  const keys = nodes.map((n) => n.key).sort(compareKeys)
  if (keys.length === 0) return { positions: {}, layers: {}, width: 0, height: 0, layerCount: 0 }
  const present = new Set(keys)
  const usable = edges.filter((e) => present.has(e.from) && present.has(e.to) && e.from !== e.to)

  const layer = hierarchyLayers(keys, usable)
  placeRest(keys, usable, layer)
  // Слои без пропусков: пустой столбец между слоями только тратит место.
  const used = [...new Set(layer.values())].sort((a, b) => a - b)
  const compact = new Map(used.map((l, i) => [l, i]))
  for (const k of keys) layer.set(k, compact.get(layer.get(k)!)!)

  const columns: string[][] = used.map(() => [])
  for (const k of keys) columns[layer.get(k)!].push(k)
  orderLayers(columns, usable, layer)

  const tallest = Math.max(...columns.map((c) => c.length))
  const innerH = tallest * NODE_H + (tallest - 1) * ROW_GAP
  const positions: Record<string, Point> = {}
  const layers: Record<string, number> = {}
  columns.forEach((col, ci) => {
    const colH = col.length * NODE_H + (col.length - 1) * ROW_GAP
    const top = PADDING + (innerH - colH) / 2
    col.forEach((k, ri) => {
      positions[k] = { x: PADDING + ci * (NODE_W + COL_GAP), y: top + ri * (NODE_H + ROW_GAP) }
      layers[k] = ci
    })
  })
  return {
    positions, layers,
    width: PADDING * 2 + columns.length * NODE_W + (columns.length - 1) * COL_GAP,
    height: PADDING * 2 + innerH,
    layerCount: columns.length,
  }
}

/** Путь ребра между вершинами — кубическая кривая от правого края источника к левому краю цели (для обратных рёбер — по краям навстречу). */
export function edgePath(layout: MapLayout, from: string, to: string): { d: string; mx: number; my: number } | null {
  const a = layout.positions[from]
  const b = layout.positions[to]
  if (!a || !b) return null
  const forward = b.x > a.x
  const sameLayer = b.x === a.x
  const x1 = forward ? a.x + NODE_W : a.x
  const y1 = a.y + NODE_H / 2
  const x2 = forward ? b.x : sameLayer ? b.x : b.x + NODE_W
  const y2 = b.y + NODE_H / 2
  if (sameLayer) {
    // Внутрислойное ребро огибает вершины справа.
    const bulge = COL_GAP / 2 + NODE_W
    const sx = a.x + NODE_W
    return { d: `M ${sx} ${y1} C ${a.x + bulge} ${y1}, ${a.x + bulge} ${y2}, ${sx} ${y2}`, mx: a.x + NODE_W + COL_GAP / 3, my: (y1 + y2) / 2 }
  }
  const dx = Math.max(24, Math.abs(x2 - x1) / 2)
  const c1 = forward ? x1 + dx : x1 - dx
  const c2 = forward ? x2 - dx : x2 + dx
  return { d: `M ${x1} ${y1} C ${c1} ${y1}, ${c2} ${y2}, ${x2} ${y2}`, mx: (x1 + x2) / 2, my: (y1 + y2) / 2 }
}
