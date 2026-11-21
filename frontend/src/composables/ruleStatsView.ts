// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Подача статистики правил в интерфейсе (ADR-0034 §7): пометки, а не диагноз. Статистика применяется на стороне
 * просмотра к неизменному отчёту; уровень уверенности она не меняет, рекомендации не скрывает и не выключает.
 * Доля показывается только при `trusted` (выборка не меньше порога), иначе — только число случаев.
 */
import { formatNumber } from '../i18n/format'
import { t } from '../i18n'
import type { Hypothesis, HypothesisStat, Recommendation, RecommendationStat, RuleStats } from '../types/incident'

/** Ключ статистики — (ruleId, epoch, code); у отчёта версии 1 `ruleId` пуст, и статистики для него нет. */
const key = (ruleId: string | undefined, epoch: number | undefined, code: string) => `${ruleId ?? ''}|${epoch ?? 0}|${code}`

export function hypothesisStatFor(stats: RuleStats | null | undefined, h: Hypothesis): HypothesisStat | undefined {
  if (!stats || !h.ruleId) return undefined
  const k = key(h.ruleId, h.ruleEpoch, h.code)
  return stats.hypotheses?.find((s) => key(s.ruleId, s.epoch, s.code) === k)
}

export function recommendationStatFor(stats: RuleStats | null | undefined, r: Recommendation): RecommendationStat | undefined {
  if (!stats || !r.ruleId) return undefined
  const k = key(r.ruleId, r.ruleEpoch, r.code)
  return stats.recommendations?.find((s) => key(s.ruleId, s.epoch, s.code) === k)
}

/** Типичное время устранения словами («30 минут», «2 часа»); пусто, если данных нет. */
export function formatDuration(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds <= 0) return ''
  const units: [number, Intl.NumberFormatOptions['unit']][] = [[86400, 'day'], [3600, 'hour'], [60, 'minute']]
  for (const [size, unit] of units) {
    if (seconds >= size) return formatNumber(Math.round(seconds / size), { style: 'unit', unit, unitDisplay: 'long', maximumFractionDigits: 0 })
  }
  return formatNumber(Math.round(seconds), { style: 'unit', unit: 'second', unitDisplay: 'long', maximumFractionDigits: 0 })
}

/** Доля в процентах — только когда ей можно верить. */
export function percent(value: number | undefined, trusted: boolean): string {
  return trusted && value !== undefined ? formatNumber(value, { style: 'percent', maximumFractionDigits: 0 }) : ''
}

/** Пометки у гипотезы: «подтверждалась в N из M …», типичное время, повторение. Пустой список — нечего показывать. */
export function hypothesisNotes(s: HypothesisStat | undefined): string[] {
  if (!s || s.sample <= 0) return []
  const out: string[] = []
  if (s.trusted) {
    out.push(t('incidentsInsights.stats.confirmedIn', { n: s.confirmed, m: s.sample }))
    const dur = formatDuration(s.medianResolveSeconds)
    if (dur) out.push(t('incidentsInsights.stats.typicalTime', { time: dur }))
    if (s.recurred > 0) out.push(t('incidentsInsights.stats.recurred', { n: s.recurred, m: s.resolved }))
  } else {
    // Мало случаев: честно говорим «мало», процента нет.
    out.push(t('incidentsInsights.stats.fewCases', { m: s.sample }))
  }
  return out
}

/** Пометка у рекомендации: «ранее не помегала» (только при trusted), «помегала в N из M» или число случаев. */
export function recommendationNote(s: RecommendationStat | undefined): { text: string; warn: boolean } | null {
  if (!s || s.sample <= 0) return null
  if (!s.trusted) return { text: t('incidentsInsights.stats.fewCases', { m: s.sample }), warn: false }
  if (s.previouslyUnhelpful) return { text: t('incidentsInsights.stats.previouslyUnhelpful', { n: s.helpful, m: s.sample }), warn: true }
  return { text: t('incidentsInsights.stats.helpedIn', { n: s.helpful, m: s.sample }), warn: false }
}

const CONF_RANK: Record<string, number> = { high: 2, medium: 1, low: 0 }

/**
 * Порядок гипотез: уровень уверенности правила не трогаем — переставляем только подряд идущие гипотезы одного уровня,
 * по точности в этом кластере. Гипотеза с недостаточной выборкой считается нейтральной (0,5): ей не дают ни преимущества,
 * ни штрафа за отсутствие данных. Порядок стабильный: при равенстве остаётся исходный.
 */
export function rankWithinConfidence<T extends { h: Hypothesis }>(items: T[], stats: RuleStats | null | undefined): T[] {
  if (!stats) return items
  const score = (h: Hypothesis) => {
    const s = hypothesisStatFor(stats, h)
    return s?.trusted && s.precision !== undefined ? s.precision : 0.5
  }
  const out: T[] = []
  let i = 0
  while (i < items.length) {
    let j = i + 1
    while (j < items.length && (CONF_RANK[items[j].h.confidence] ?? 0) === (CONF_RANK[items[i].h.confidence] ?? 0)) j++
    out.push(...items.slice(i, j).map((it, idx) => ({ it, idx })).sort((a, b) => score(b.it.h) - score(a.it.h) || a.idx - b.idx).map((x) => x.it))
    i = j
  }
  return out
}
