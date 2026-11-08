// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Чистая логика Flux CD: ранг проблемности, сводка дашборда, форматирование ревизий и ссылок.
 * Модуль не импортирует ни Vue, ни Wails, ни i18n: возвращает только структуры и коды, а подписи
 * для пользователя собирает вызывающий код (образец — argocd.ts).
 */
import type { FluxObjectInfo, FluxRef, FluxState } from '../types/flux'

/**
 * Ранг состояния: 0 — всё хорошо, чем больше, тем важнее показать объект.
 * Failed и Stalled выше Reconciling и Suspended. Stalled (контроллер сдался и ждёт правки) выше Failed
 * (идут повторы). Неизвестное состояние ранжируется как Unknown.
 */
const STATE_RANK: ReadonlyMap<string, number> = new Map<string, number>([
  ['Ready', 0],
  ['Suspended', 1],
  ['Unknown', 1],
  ['Reconciling', 2],
  ['Failed', 3],
  ['Stalled', 4],
])

const UNKNOWN_RANK = STATE_RANK.get('Unknown') ?? 1

export function fluxStateRank(state?: string | null): number {
  return (state != null ? STATE_RANK.get(state) : undefined) ?? UNKNOWN_RANK
}

/** 0 — объект в порядке; больше — приоритетнее (Stalled и Failed выше Reconciling и Suspended) */
export function problemRank(obj: Pick<FluxObjectInfo, 'state'>): number {
  return fluxStateRank(obj.state)
}

// --- форматирование ---------------------------------------------------------------------------------

/** Длина хэша в символах для алгоритмов, которые встречаются в ревизиях Flux */
const HASH_LEN: Readonly<Record<string, number>> = { sha1: 40, sha256: 64, sha512: 128 }
const SHORT_HASH = 7

/** [ветка@][алгоритм:]hex: ветка и тег целиком допускают любые символы, хэш — только hex */
const REVISION_RE = /^(?:(.+)@)?(?:(sha1|sha256|sha512):)?([0-9a-f]+)$/i

/**
 * Короткая ревизия: хэш до 7 символов, ветка и тег целиком.
 *   main@sha1:<40 hex>       -> main@0123456
 *   latest@sha256:<64 hex>   -> latest@0123456
 *   sha256:<64 hex>          -> sha256:0123456
 *   <40 или 64 hex>          -> 0123456
 *   v1.2.3                   -> v1.2.3
 * Всё, что не похоже на ревизию с хэшем правильной длины, возвращается без изменений.
 */
export function shortRevision(rev?: string | null): string {
  const s = rev?.trim()
  if (!s) return ''
  const m = REVISION_RE.exec(s)
  if (!m) return s
  const [, branch, algo, hash] = m
  if (algo) {
    if (hash.length !== HASH_LEN[algo.toLowerCase()]) return s
    const short = hash.slice(0, SHORT_HASH)
    return branch ? `${branch}@${short}` : `${algo}:${short}`
  }
  // голый хэш без алгоритма: только sha1 (40) или sha256 (64) и без ветки, иначе это тег вроде «deadbeef»
  if (branch || (hash.length !== HASH_LEN.sha1 && hash.length !== HASH_LEN.sha256)) return s
  return hash.slice(0, SHORT_HASH)
}

/** Ссылка на источник: Kind/name, с namespace — Kind/namespace/name; без имени — пусто */
export function sourceRefLabel(ref?: FluxRef | null): string {
  if (!ref?.name) return ''
  return [ref.kind, ref.namespace, ref.name].filter(Boolean).join('/')
}

/** Цель зависимости (у dependsOn вид обычно пуст): namespace/name или имя; с видом — Kind/... */
export function dependsOnLabel(ref?: FluxRef | null): string {
  return sourceRefLabel(ref)
}

/** Чарт HelmRelease: имя@версия, без версии — имя, без имени — пусто */
export function chartLabel(name?: string | null, version?: string | null): string {
  if (!name) return ''
  return version ? `${name}@${version}` : name
}

// --- сводка дашборда --------------------------------------------------------------------------------

/** Поля объекта, нужные сводке: подходит любой DTO Flux (Kustomization, HelmRelease, источник…) */
export type FluxSummaryItem = Pick<FluxObjectInfo, 'name' | 'namespace' | 'state' | 'sourceRef' | 'lastReconcileAt'>

export interface FluxGroupCount {
  /** namespace или метка источника; пустое имя в bySource — объекты без источника */
  name: string
  total: number
  problems: number
}

export interface FluxSummary<T extends FluxSummaryItem = FluxSummaryItem> {
  total: number
  ready: number
  reconciling: number
  failed: number
  stalled: number
  suspended: number
  /** Unknown и любые состояния вне контракта */
  unknown: number
  /** объекты с problemRank > 0, самые проблемные первыми */
  problems: T[]
  byNamespace: FluxGroupCount[]
  /** ключ — sourceRefLabel; namespace ссылки без явного значения берётся из самого объекта */
  bySource: FluxGroupCount[]
  /** последние reconcile (по lastReconcileAt, новые первыми); объекты без корректного времени пропускаются */
  recent: T[]
}

const byLocale = (a: string, b: string) => a.localeCompare(b)

function group<T extends FluxSummaryItem>(items: readonly T[], key: (o: T) => string): FluxGroupCount[] {
  const m = new Map<string, FluxGroupCount>()
  for (const o of items) {
    const k = key(o)
    const g = m.get(k) ?? { name: k, total: 0, problems: 0 }
    g.total++
    if (problemRank(o) > 0) g.problems++
    m.set(k, g)
  }
  return [...m.values()].sort((x, y) => y.problems - x.problems || y.total - x.total || byLocale(x.name, y.name))
}

const timeOf = (o: Pick<FluxSummaryItem, 'lastReconcileAt'>): number => Date.parse(o.lastReconcileAt)

/** Сводка по списку объектов Flux (без мутации входного массива) */
export function summarizeFlux<T extends FluxSummaryItem>(
  items: readonly T[] | null | undefined,
  recentLimit = 8,
): FluxSummary<T> {
  const list = items ?? []
  const count = (state: FluxState) => list.filter(o => o.state === state).length
  const known = new Set<string>(['Ready', 'Reconciling', 'Failed', 'Stalled', 'Suspended'])

  const problems = list
    .filter(o => problemRank(o) > 0)
    .sort((x, y) => problemRank(y) - problemRank(x) || byLocale(x.namespace, y.namespace) || byLocale(x.name, y.name))

  const recent = list
    .filter(o => Number.isFinite(timeOf(o)))
    .sort((x, y) => timeOf(y) - timeOf(x) || byLocale(x.namespace, y.namespace) || byLocale(x.name, y.name))
    .slice(0, Math.max(0, recentLimit))

  return {
    total: list.length,
    ready: count('Ready'),
    reconciling: count('Reconciling'),
    failed: count('Failed'),
    stalled: count('Stalled'),
    suspended: count('Suspended'),
    unknown: list.filter(o => !known.has(o.state)).length,
    problems,
    byNamespace: group(list, o => o.namespace),
    bySource: group(list, o => sourceRefLabel(o.sourceRef && { ...o.sourceRef, namespace: o.sourceRef.namespace || o.namespace })),
    recent,
  }
}
