// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { formatDuration, hypothesisNotes, hypothesisStatFor, percent, rankWithinConfidence, recommendationNote, recommendationStatFor } from './ruleStatsView'
import { ruleStatsFixture } from '../components/incidents/__fixtures__'
import type { Hypothesis, HypothesisStat, RecommendationStat, RuleStats } from '../types/incident'

const ref = { kind: 'Pod', name: 'p' }
const hyp = (id: string, code: string, confidence: Hypothesis['confidence'] = 'medium'): Hypothesis => ({
  kind: 'hypothesis', id, code, resource: ref, confidence, supporting: [], contradicting: [], ruleId: code, ruleEpoch: 1,
})
const stat = (code: string, over: Partial<HypothesisStat> = {}): HypothesisStat => ({
  ruleId: code, epoch: 1, code, confirmed: 5, rejected: 1, sample: 6, trusted: true, resolved: 5, recurred: 0, precision: 0.8, ...over,
})
const stats = (...h: HypothesisStat[]): RuleStats => ({ ...ruleStatsFixture, hypotheses: h })

describe('поиск статистики по ключу (ruleId, epoch, code)', () => {
  it('находит по ключу; у отчёта без ruleId статистики нет', () => {
    expect(hypothesisStatFor(ruleStatsFixture, hyp('h', 'crash.oom_killed'))?.sample).toBe(8)
    expect(hypothesisStatFor(ruleStatsFixture, { ...hyp('h', 'crash.oom_killed'), ruleId: undefined })).toBeUndefined()
    expect(hypothesisStatFor(ruleStatsFixture, { ...hyp('h', 'crash.oom_killed'), ruleEpoch: 2 })).toBeUndefined()
    expect(hypothesisStatFor(null, hyp('h', 'crash.oom_killed'))).toBeUndefined()
    expect(recommendationStatFor(ruleStatsFixture, { kind: 'recommendation', id: 'r', code: 'rec.create_storageclass', resource: ref, hypotheses: [], ruleId: 'storage.class_missing', ruleEpoch: 1 })?.helpful).toBe(4)
  })
})

describe('пометки гипотез', () => {
  it('trusted: «в N из M», типичное время и повторения; процента в пометке нет', () => {
    const n = hypothesisNotes(stat('a', { confirmed: 6, sample: 8, medianResolveSeconds: 1800, recurred: 1, resolved: 6 }))
    expect(n[0]).toBe('confirmed in 6 of 8 cases here')
    expect(n[1]).toContain('30 minutes')
    expect(n[2]).toBe('came back after 1 of 6 resolutions')
    expect(n.join(' ')).not.toContain('%')
  })
  it('мало случаев: только число, без процента и без «в N из M»', () => {
    const n = hypothesisNotes(stat('a', { trusted: false, sample: 2, precision: undefined }))
    expect(n).toEqual(['2 cases so far: too few for a percentage'])
  })
  it('нет статистики или нулевая выборка — нет пометок', () => {
    expect(hypothesisNotes(undefined)).toEqual([])
    expect(hypothesisNotes(stat('a', { sample: 0 }))).toEqual([])
  })
})

describe('пометки рекомендаций', () => {
  const rec = (over: Partial<RecommendationStat>): RecommendationStat => ({ ruleId: 'r', epoch: 1, code: 'c', helpful: 4, useless: 1, ratio: 0.8, sample: 5, trusted: true, ...over })
  it('помогала, ранее не помогала (с предупреждением), мало случаев, нет данных', () => {
    expect(recommendationNote(rec({}))).toEqual({ text: 'helped in 4 of 5 cases here', warn: false })
    expect(recommendationNote(rec({ helpful: 0, useless: 4, sample: 4, previouslyUnhelpful: true }))).toEqual({ text: 'previously did not help here (helped in 0 of 4)', warn: true })
    expect(recommendationNote(rec({ trusted: false, sample: 3, previouslyUnhelpful: true }))).toEqual({ text: '3 cases so far: too few for a percentage', warn: false })
    expect(recommendationNote(undefined)).toBeNull()
  })
})

describe('порядок гипотез', () => {
  const items = (...hs: Hypothesis[]) => hs.map((h) => ({ h }))
  const ids = (xs: { h: Hypothesis }[]) => xs.map((x) => x.h.id)

  it('внутри одного уровня — по точности; доверенная слабая правило уходит ниже нейтральной', () => {
    const s = stats(stat('a', { precision: 0.4 }), stat('b', { precision: 0.9 }))
    const out = rankWithinConfidence(items(hyp('1', 'a'), hyp('2', 'b'), hyp('3', 'c')), s)
    expect(ids(out)).toEqual(['2', '3', '1'])
  })
  it('уровни уверенности не смешиваются: высокая остаётся выше, как её отдал бэкенд', () => {
    const s = stats(stat('a', { precision: 0.1 }), stat('b', { precision: 0.99 }))
    const out = rankWithinConfidence(items(hyp('1', 'a', 'high'), hyp('2', 'b', 'low')), s)
    expect(ids(out)).toEqual(['1', '2'])
  })
  it('без статистики, с недоверенной статистикой и при равенстве порядок исходный', () => {
    const list = items(hyp('1', 'a'), hyp('2', 'b'), hyp('3', 'c'))
    expect(ids(rankWithinConfidence(list, null))).toEqual(['1', '2', '3'])
    const weak = stats(stat('a', { trusted: false, precision: undefined, sample: 2 }), stat('b', { trusted: false, precision: undefined, sample: 3 }))
    expect(ids(rankWithinConfidence(list, weak))).toEqual(['1', '2', '3'])
  })
  it('гипотезы не теряются', () => {
    const s = stats(stat('a', { precision: 0.2 }))
    expect(rankWithinConfidence(items(hyp('1', 'a'), hyp('2', 'b')), s)).toHaveLength(2)
  })
})

describe('числа', () => {
  it('процент только при доверии', () => {
    expect(percent(0.75, true)).toBe('75%')
    expect(percent(0.75, false)).toBe('')
    expect(percent(undefined, true)).toBe('')
  })
  it('длительность словами', () => {
    expect(formatDuration(1800)).toBe('30 minutes')
    expect(formatDuration(7200)).toBe('2 hours')
    expect(formatDuration(172800)).toBe('2 days')
    expect(formatDuration(45)).toBe('45 seconds')
    expect(formatDuration(undefined)).toBe('')
    expect(formatDuration(0)).toBe('')
  })
})
