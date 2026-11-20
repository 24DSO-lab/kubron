// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Сборка и проверка отметки результата (ADR-0034 §2). Обязателен только результат: всё, что человек не отметил,
 * в запрос не попадает («не отмечено» = отсутствие поля), чтобы статистика не получала ложных «не помогло».
 */
import type { CorrelationVerdictValue, OutcomeInput, OutcomeResult } from '../types/incident'

/** internal/incident.MaxOutcomeRefs: сколько гипотез/рекомендаций сервер принимает в одном списке. */
export const MAX_OUTCOME_REFS = 20
export const MAX_NOTE_LENGTH = 2000

export type RecMark = 'none' | 'helpful' | 'useless'

export interface OutcomeFormState {
  result: OutcomeResult
  confirmed: string
  rejected: string[]
  /** id рекомендации → отметка; нет записи = «не отмечено» */
  recs: Record<string, RecMark>
  otherAction: boolean
  /** значение поля datetime-local: «ГГГГ-ММ-ДДTЧЧ:ММ» (локальное время) или пусто */
  resolvedAt: string
  correlationCandidate: string
  /** '' = не оценено */
  correlationVerdict: CorrelationVerdictValue | ''
  note: string
}

export const emptyOutcomeState = (): OutcomeFormState => ({
  result: 'resolved', confirmed: '', rejected: [], recs: {}, otherAction: false, resolvedAt: '',
  correlationCandidate: '', correlationVerdict: '', note: '',
})

/** Значение для `<input type="datetime-local">` из ISO-времени (локальный пояс, минуты). */
export function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

const parseLocal = (v: string): Date | null => {
  if (!v) return null
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? null : d
}

/** Запрос для IncidentMarkOutcome: только то, что человек действительно отметил. */
export function buildOutcomeInput(s: OutcomeFormState): OutcomeInput {
  const out: OutcomeInput = { result: s.result }
  if (s.confirmed) out.confirmedHypothesisId = s.confirmed
  // Подтверждённая гипотеза не может быть отвергнутой одновременно.
  const rejected = s.rejected.filter((id) => id !== s.confirmed)
  if (rejected.length) out.rejectedHypothesisIds = rejected
  const helpful = Object.keys(s.recs).filter((id) => s.recs[id] === 'helpful')
  const useless = Object.keys(s.recs).filter((id) => s.recs[id] === 'useless')
  if (helpful.length) out.helpfulRecommendationIds = helpful
  if (useless.length) out.uselessRecommendationIds = useless
  // «Помогло другое» и «когда устранено» осмысленны только для устранённой проблемы.
  if (s.result === 'resolved') {
    if (s.otherAction) out.otherAction = true
    const at = parseLocal(s.resolvedAt)
    if (at) out.resolvedAt = at.toISOString()
  }
  if (s.correlationVerdict && s.correlationCandidate) out.correlation = { candidateId: s.correlationCandidate, verdict: s.correlationVerdict }
  const note = s.note.trim()
  if (note) out.note = note
  return out
}

export type OutcomeError = '' | 'tooMany' | 'noteTooLong' | 'resolvedBeforeOpen' | 'resolvedInFuture' | 'resolvedInvalid'

/** Проверка до отправки (сервер проверяет заново): понятная ошибка вместо отказа бэкенда. */
export function validateOutcome(s: OutcomeFormState, ctx: { openedAt: string; now?: Date }): OutcomeError {
  const input = buildOutcomeInput(s)
  const lists = [input.rejectedHypothesisIds, input.helpfulRecommendationIds, input.uselessRecommendationIds]
  if (lists.some((l) => (l?.length ?? 0) > MAX_OUTCOME_REFS)) return 'tooMany'
  if (s.note.trim().length > MAX_NOTE_LENGTH) return 'noteTooLong'
  if (s.result === 'resolved' && s.resolvedAt) {
    const at = parseLocal(s.resolvedAt)
    if (!at) return 'resolvedInvalid'
    const opened = new Date(ctx.openedAt)
    // datetime-local точен до минуты: расследование, открытое в 10:00:30, считается открытым с 10:00.
    if (!Number.isNaN(opened.getTime()) && at.getTime() < Math.floor(opened.getTime() / 60000) * 60000) return 'resolvedBeforeOpen'
    if (at.getTime() > (ctx.now ?? new Date()).getTime()) return 'resolvedInFuture'
  }
  return ''
}
