// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { MAX_OUTCOME_REFS, buildOutcomeInput, emptyOutcomeState, toLocalInput, validateOutcome, type OutcomeFormState } from './outcomeInput'

const st = (over: Partial<OutcomeFormState> = {}): OutcomeFormState => ({ ...emptyOutcomeState(), ...over })
const ctx = { openedAt: '2026-11-19T10:00:00Z', now: new Date('2026-11-19T12:00:00Z') }
const local = (iso: string) => toLocalInput(iso)

describe('buildOutcomeInput', () => {
  it('без отметок уходит только результат', () => {
    expect(buildOutcomeInput(st())).toEqual({ result: 'resolved' })
    expect(buildOutcomeInput(st({ result: 'inconclusive' }))).toEqual({ result: 'inconclusive' })
  })

  it('«не отмечено» у рекомендации — отсутствие поля, а не «не помогло»', () => {
    const out = buildOutcomeInput(st({ recs: { r1: 'none', r2: 'none' } }))
    expect(out).toEqual({ result: 'resolved' })
    expect('uselessRecommendationIds' in out).toBe(false)
    expect('helpfulRecommendationIds' in out).toBe(false)
  })

  it('помогло и не помогло попадают в свои списки', () => {
    const out = buildOutcomeInput(st({ recs: { r1: 'helpful', r2: 'useless', r3: 'none', r4: 'helpful' } }))
    expect(out.helpfulRecommendationIds).toEqual(['r1', 'r4'])
    expect(out.uselessRecommendationIds).toEqual(['r2'])
  })

  it('подтверждённая и отвергнутые гипотезы; подтверждённая не может быть отвергнута', () => {
    const out = buildOutcomeInput(st({ confirmed: 'h1', rejected: ['h1', 'h2'] }))
    expect(out.confirmedHypothesisId).toBe('h1')
    expect(out.rejectedHypothesisIds).toEqual(['h2'])
    expect(buildOutcomeInput(st({ rejected: [] })).rejectedHypothesisIds).toBeUndefined()
    expect(buildOutcomeInput(st({ confirmed: 'h1', rejected: ['h1'] })).rejectedHypothesisIds).toBeUndefined()
  })

  it('«помогло другое» и «когда устранено» — только для resolved', () => {
    const at = local('2026-11-19T11:00:00Z')
    const resolved = buildOutcomeInput(st({ otherAction: true, resolvedAt: at }))
    expect(resolved.otherAction).toBe(true)
    expect(new Date(resolved.resolvedAt!).getTime()).toBe(new Date(at).getTime())
    for (const result of ['unresolved', 'inconclusive'] as const) {
      const out = buildOutcomeInput(st({ result, otherAction: true, resolvedAt: at }))
      expect(out).toEqual({ result })
    }
    expect(buildOutcomeInput(st({ otherAction: false })).otherAction).toBeUndefined()
    expect(buildOutcomeInput(st({ resolvedAt: '' })).resolvedAt).toBeUndefined()
  })

  it('оценка корреляции — только вместе с кандидатом и явным вердиктом', () => {
    expect(buildOutcomeInput(st({ correlationCandidate: 'c1' })).correlation).toBeUndefined()
    expect(buildOutcomeInput(st({ correlationVerdict: 'confirmed' })).correlation).toBeUndefined()
    expect(buildOutcomeInput(st({ correlationCandidate: 'c1', correlationVerdict: 'unknown' })).correlation).toEqual({ candidateId: 'c1', verdict: 'unknown' })
  })

  it('комментарий обрезается по краям, пустой не отправляется', () => {
    expect(buildOutcomeInput(st({ note: '  ' })).note).toBeUndefined()
    expect(buildOutcomeInput(st({ note: '  ok \n' })).note).toBe('ok')
  })
})

describe('validateOutcome', () => {
  it('пустая форма и корректные отметки проходят', () => {
    expect(validateOutcome(st(), ctx)).toBe('')
    expect(validateOutcome(st({ resolvedAt: local('2026-11-19T11:00:00Z') }), ctx)).toBe('')
  })

  it('время устранения: не раньше открытия и не в будущем', () => {
    expect(validateOutcome(st({ resolvedAt: local('2026-11-19T09:00:00Z') }), ctx)).toBe('resolvedBeforeOpen')
    expect(validateOutcome(st({ resolvedAt: local('2026-11-19T13:00:00Z') }), ctx)).toBe('resolvedInFuture')
    expect(validateOutcome(st({ resolvedAt: 'не дата' }), ctx)).toBe('resolvedInvalid')
    // для не-resolved поле игнорируется и ошибки не даёт
    expect(validateOutcome(st({ result: 'unresolved', resolvedAt: local('2026-11-19T09:00:00Z') }), ctx)).toBe('')
  })

  it('расследование, открытое в середине минуты, допускает устранение в ту же минуту', () => {
    const c = { openedAt: '2026-11-19T10:00:45Z', now: new Date('2026-11-19T12:00:00Z') }
    expect(validateOutcome(st({ resolvedAt: local('2026-11-19T10:00:00Z') }), c)).toBe('')
  })

  it('не больше 20 отметок в списке и предел комментария', () => {
    const many = Array.from({ length: MAX_OUTCOME_REFS + 1 }, (_, i) => `h${i}`)
    expect(validateOutcome(st({ rejected: many }), ctx)).toBe('tooMany')
    expect(validateOutcome(st({ rejected: many.slice(0, MAX_OUTCOME_REFS) }), ctx)).toBe('')
    expect(validateOutcome(st({ note: 'x'.repeat(2001) }), ctx)).toBe('noteTooLong')
  })
})

describe('toLocalInput', () => {
  it('формат «ГГГГ-ММ-ДДTЧЧ:ММ»; мусор — пустая строка', () => {
    expect(toLocalInput('2026-11-19T10:05:00Z')).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    expect(toLocalInput('x')).toBe('')
  })
})
