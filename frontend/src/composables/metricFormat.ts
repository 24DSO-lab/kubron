// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Показ значений фактов `metric.*` (ADR-0032 §6): значение, единица, окно, агрегат, лимит.
 * Бэкенд отдаёт параметры строками с фиксированной точностью (`value`, `unit`, `agg`, `window`, `limit`);
 * форматирование чисел идёт через i18n/format.ts, поэтому следует языку интерфейса.
 */
import { formatNumber } from '../i18n/format'
import { locale, t, te } from '../i18n'
import type { Fact } from '../types/incident'

export const METRIC_PREFIX = 'metric.'

export const isMetricFact = (f: Fact): boolean => f.source?.kind === 'metric' || f.code.startsWith(METRIC_PREFIX)

const toNumber = (s: string | undefined): number | undefined => {
  if (s === undefined || s.trim() === '') return undefined
  const n = Number(s)
  return Number.isFinite(n) ? n : undefined
}

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const

export function formatBytes(bytes: number): string {
  let n = bytes
  let i = 0
  while (n >= 1024 && i < BYTE_UNITS.length - 1) {
    n /= 1024
    i++
  }
  return `${formatNumber(n, { maximumFractionDigits: i === 0 ? 0 : 1 })} ${BYTE_UNITS[i]}`
}

/** Окно вида «1h», «15m», «30s» словами на языке интерфейса; нераспознанное — как есть (это длительность, а не код). */
export function formatWindow(w: string | undefined): string {
  const m = /^(\d+)([hms])$/.exec((w ?? '').trim())
  if (!m) return w ?? ''
  const unit = { h: 'hour', m: 'minute', s: 'second' }[m[2] as 'h' | 'm' | 's']
  try {
    return new Intl.NumberFormat(locale.value, { style: 'unit', unit, unitDisplay: 'long' }).format(Number(m[1]))
  } catch {
    return w ?? ''
  }
}

/** Значение по единице: доля — проценты, счётчик — целое, флаг — словами; неразобранное значение — «—». */
export function formatMetricValue(unit: string | undefined, value: string | undefined): string {
  const n = toNumber(value)
  if (n === undefined) return '—'
  switch (unit) {
    case 'ratio':
      return formatNumber(n, { style: 'percent', maximumFractionDigits: 0 })
    case 'count':
      return formatNumber(Math.round(n), { maximumFractionDigits: 0 })
    case 'flag':
      return t(n >= 1 ? 'incidentsIntel.metric.flagOn' : 'incidentsIntel.metric.flagOff')
    default:
      return formatNumber(n, { maximumFractionDigits: 3 })
  }
}

/** Лимит: память — в байтах, CPU — в ядрах; для остальных сигналов лимита нет. */
export function formatLimit(signal: string, limit: string | undefined): string {
  const n = toNumber(limit)
  if (n === undefined) return ''
  if (signal.includes('memory')) return formatBytes(n)
  if (signal.includes('cpu')) return `${formatNumber(n, { maximumFractionDigits: 3 })} ${t('incidentsIntel.metric.cores')}`
  return ''
}

export interface MetricLine {
  id: string
  signal: string
  /** Целая строка: «97% · пик за 1 час · лимит 256 MiB» */
  text: string
}

/** Строка факта метрики; факт без числового значения даёт «—», а не пустоту. */
export function metricLine(f: Fact): MetricLine {
  const p = f.params ?? {}
  const signal = p.signal || f.source?.signal || f.code.replace(METRIC_PREFIX, '')
  const aggKey = `incidentsIntel.metric.agg.${p.agg ?? ''}`
  const agg = t(te(aggKey) ? aggKey : 'incidentsIntel.metric.agg.other')
  const value = formatMetricValue(p.unit, p.value)
  const window = formatWindow(p.window)
  let text = window ? t('incidentsIntel.metric.summary', { value, agg, window }) : t('incidentsIntel.metric.summaryNoWindow', { value, agg })
  const limit = formatLimit(signal, p.limit)
  if (limit) text += ` · ${t('incidentsIntel.metric.limit', { limit })}`
  return { id: f.id, signal, text }
}
