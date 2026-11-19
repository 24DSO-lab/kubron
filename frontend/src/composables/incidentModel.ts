// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { t, te } from '../i18n'
import type { Evidence, Fact, FragmentView, Hypothesis, Incident, IncidentSettings, Params } from '../types/incident'

/** Раздел перевода для кода диагностики. Коды содержат точки, в locales они записаны через «_» (плоские ключи). */
export type CodeSection = 'symptoms' | 'facts' | 'hypotheses' | 'recommendations' | 'steps' | 'missing'

export function codeKey(section: CodeSection, code: string): string {
  return `incidents.${section}.${code.replaceAll('.', '_')}`
}

/** Параметры для подстановки в перевод: значения — строки, пропущенные поля не должны давать «undefined». */
export function safeParams(p: Params | undefined): Params {
  const out: Params = {}
  for (const [k, v] of Object.entries(p ?? {})) out[k] = typeof v === 'string' ? v : String(v ?? '')
  return out
}

/** Ссылка на лог из факта находит состояние доказательства: связь по «под, контейнер, экземпляр», не по номеру факта. */
export function evidenceFor(incident: Incident, fact: Fact): Evidence | undefined {
  if (!fact.log) return undefined
  return incident.evidence.find(
    (e) => e.pod === fact.resource.name && e.container === fact.log!.container && !!e.previous === !!fact.log!.previous,
  )
}

export function fragmentOf(fragments: FragmentView[], ev: Evidence | undefined): FragmentView | undefined {
  return ev?.fragmentId ? fragments.find((f) => f.id === ev.fragmentId) : undefined
}

/** Находит факты гипотезы по идентификаторам; несуществующий идентификатор пропускается, а не роняет экран. */
export function factsByIds(facts: Fact[], ids: string[] | undefined): Fact[] {
  const byId = new Map(facts.map((f) => [f.id, f]))
  return (ids ?? []).flatMap((id) => {
    const f = byId.get(id)
    return f ? [f] : []
  })
}

export function hypothesisById(list: Hypothesis[], id: string | undefined): Hypothesis | undefined {
  return id ? list.find((h) => h.id === id) : undefined
}

/** Выбор показываемой уверенности: неизвестное значение трактуется осторожно — как низкая. */
export function confidenceLevel(c: string | undefined): 'low' | 'medium' | 'high' {
  return c === 'high' || c === 'medium' ? c : 'low'
}

/** Настройки из текстового поля: namespace по одному в строке, без пустых и повторов. */
export function parseNamespaces(text: string): string[] {
  const seen = new Set<string>()
  for (const line of text.split(/[\n,]/)) {
    const ns = line.trim()
    if (ns) seen.add(ns)
  }
  return [...seen]
}

/** Проверка настроек до отправки (бэкенд проверяет заново): возвращает ключ ошибки или ''. */
export function validateSettings(s: IncidentSettings, maxFragmentDays: number, maxIncidentDays: number): string {
  if (!Number.isInteger(s.fragmentRetentionDays) || s.fragmentRetentionDays < 1 || s.fragmentRetentionDays > maxFragmentDays)
    return 'fragmentDays'
  if (!Number.isInteger(s.incidentRetentionDays) || s.incidentRetentionDays < 1 || s.incidentRetentionDays > maxIncidentDays)
    return 'incidentDays'
  return ''
}

/** Приводит ответ бэкенда к форме без null: Go отдаёт nil-срезы как null. */
export function normalizeIncident(i: Incident): Incident {
  return {
    ...i,
    events: i.events ?? [],
    evidence: i.evidence ?? [],
    report: {
      ...i.report,
      symptoms: i.report?.symptoms ?? [],
      resources: i.report?.resources ?? [],
      facts: i.report?.facts ?? [],
      hypotheses: (i.report?.hypotheses ?? []).map((h) => ({ ...h, supporting: h.supporting ?? [], contradicting: h.contradicting ?? [] })),
      recommendations: (i.report?.recommendations ?? []).map((r) => ({ ...r, hypotheses: r.hypotheses ?? [] })),
      steps: (i.report?.steps ?? []).map((s) => ({ ...s, hypotheses: s.hypotheses ?? [] })),
      missing: i.report?.missing ?? [],
    },
  }
}

/**
 * Пространства имён locale: имя файла = пространство (`incidentsMetrics.json` → `incidentsMetrics.*`).
 * Тексты кодов лежат у владельцев кодов (locales/README.md), поэтому код ищется во всех по порядку.
 */
const CODE_NAMESPACES = ['incidents', 'incidentsMetrics', 'incidentsChain', 'incidentsCorrelation'] as const
/** Префикс кода → пространство, где искать в первую очередь (у разных владельцев одинаковые причины значат разное). */
const PREFERRED_NAMESPACE: Record<string, (typeof CODE_NAMESPACES)[number]> = {
  metric: 'incidentsMetrics', metrics: 'incidentsMetrics', correlation: 'incidentsCorrelation', graph: 'incidentsChain',
}

function namespacesFor(code: string): string[] {
  const first = PREFERRED_NAMESPACE[code.split('.')[0]]
  return first ? [first, ...CODE_NAMESPACES.filter((n) => n !== first)] : [...CODE_NAMESPACES]
}

/** Ключи-кандидаты для кода: по одному на пространство имён, в порядке поиска. */
export function codeKeys(section: CodeSection, code: string): string[] {
  const flat = code.replaceAll('.', '_')
  return namespacesFor(code).map((ns) => `${ns}.${section}.${flat}`)
}

/** Причина (`params.reason`) словами; неизвестная причина — безопасный текст, а не «сырой» код. */
function reasonParam(code: string, reason: string): string {
  // Причины чужих владельцев не подставляются: «forbidden» у метрик и у графа — разные фразы.
  const own = PREFERRED_NAMESPACE[code.split('.')[0]]
  const keys = [...(own ? [`${own}.reasons.${reason}`] : []), `incidents.reasons.${reason}`, `incidentsIntel.chain.unknownReasons.${reason}`]
  const hit = keys.find((k) => te(k))
  return hit ? t(hit) : t('incidentsIntel.chain.unknownReasons.other')
}

/** Текст записи отчёта по коду и параметрам; неизвестный код — безопасный текст по умолчанию, не «сырой» код. */
export function codeText(section: CodeSection, code: string, params?: Params): string {
  const p = safeParams(params)
  if (p.reason) p.reason = reasonParam(code, p.reason)
  const key = codeKeys(section, code).find((k) => te(k))
  return key ? t(key, p) : t('incidentsIntel.unknownEntry')
}

/** Причина «доказательство не сохранено» словами; неизвестная причина — как есть. */
export function reasonText(reason: string | undefined): string {
  const key = `incidents.reasons.${reason ?? ''}`
  return reason && te(key) ? t(key) : (reason ?? '')
}
