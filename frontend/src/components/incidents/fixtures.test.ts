// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { chainIncident, correlationIncident, legacyIncident, metricsIncident, noMetricsIncident, problemDetail, ruleStatsFixture } from './__fixtures__'

describe('фикстуры Kubron Intelligence', () => {
  it('цепочка содержит Missing и Unknown и кандидата', () => {
    const steps = chainIncident.report.chains!.flatMap((c) => c.path)
    expect(steps.some((s) => s.health === 'Missing')).toBe(true)
    expect(steps.some((s) => s.health === 'Unknown')).toBe(true)
    expect(chainIncident.report.chains!.some((c) => c.candidate)).toBe(true)
  })
  it('метрики и причины их отсутствия', () => {
    expect(metricsIncident.report.facts.some((f) => f.source.kind === 'metric')).toBe(true)
    expect(noMetricsIncident.report.missing).toHaveLength(7)
  })
  it('корреляция, проблема и статистика согласованы', () => {
    expect(correlationIncident.correlation!.candidates![0].total).toBe(12)
    expect(problemDetail.members).toHaveLength(problemDetail.memberCount)
    expect(ruleStatsFixture.hypotheses.some((h) => !h.trusted && h.precision === undefined)).toBe(true)
  })
  it('старый инцидент без новых полей', () => {
    const r = legacyIncident.report
    expect(r.graph).toBeUndefined()
    expect(r.chains).toBeUndefined()
    expect(legacyIncident.correlation).toBeUndefined()
    expect(legacyIncident.problemId).toBeUndefined()
  })
})
