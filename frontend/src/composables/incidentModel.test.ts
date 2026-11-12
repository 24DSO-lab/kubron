// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import {
  codeKey, codeText, confidenceLevel, evidenceFor, factsByIds, normalizeIncident, parseNamespaces, reasonText, safeParams, validateSettings,
} from './incidentModel'
import type { Fact, Incident, IncidentSettings } from '../types/incident'

const ref = { kind: 'Pod', namespace: 'shop', name: 'api-1' }

function fact(id: string, extra: Partial<Fact> = {}): Fact {
  return { kind: 'fact', id, code: 'container.restarts', resource: ref, source: { kind: 'resource', resource: ref }, observedAt: '2026-11-01T10:00:00Z', ...extra }
}

function incident(extra: Partial<Incident> = {}): Incident {
  return {
    id: 'i1', target: ref, status: 'open', openedAt: '', updatedAt: '', openedBy: 'alice', events: [], eventsUpdatedAt: '', saveLogs: true,
    evidence: [], version: 1,
    report: { ruleSetVersion: 1, target: ref, observedAt: '', symptoms: [], resources: [], facts: [], hypotheses: [], recommendations: [], steps: [], missing: [] },
    ...extra,
  }
}

describe('incidentModel', () => {
  it('codeKey: точки кода заменяются на подчёркивания', () => {
    expect(codeKey('facts', 'container.last_terminated')).toBe('incidents.facts.container_last_terminated')
    expect(codeKey('symptoms', 'none')).toBe('incidents.symptoms.none')
  })

  it('codeText: известный код переводится с параметрами, неизвестный показывается как есть', () => {
    expect(codeText('facts', 'container.restarts', { container: 'app', count: '7' })).toContain('app')
    expect(codeText('facts', 'container.restarts', { container: 'app', count: '7' })).toContain('7')
    expect(codeText('facts', 'no.such.code')).toBe('no.such.code')
    expect(reasonText('redaction_failed')).not.toBe('redaction_failed')
    expect(reasonText('mystery')).toBe('mystery')
    expect(reasonText(undefined)).toBe('')
  })

  it('safeParams не отдаёт undefined', () => {
    expect(safeParams(undefined)).toEqual({})
    expect(safeParams({ a: 'x', b: undefined as unknown as string })).toEqual({ a: 'x', b: '' })
  })

  it('evidenceFor связывает лог факта с доказательством по «под, контейнер, экземпляр»', () => {
    const inc = incident({
      evidence: [
        { pod: 'api-1', container: 'app', previous: true, status: 'saved', fragmentId: 'f1', updatedAt: '' },
        { pod: 'api-1', container: 'app', status: 'not_saved', reason: 'disabled_org', updatedAt: '' },
      ],
    })
    expect(evidenceFor(inc, fact('f1', { log: { container: 'app', previous: true } }))?.fragmentId).toBe('f1')
    expect(evidenceFor(inc, fact('f2', { log: { container: 'app' } }))?.reason).toBe('disabled_org')
    expect(evidenceFor(inc, fact('f3', { log: { container: 'sidecar' } }))).toBeUndefined()
    expect(evidenceFor(inc, fact('f4'))).toBeUndefined()
  })

  it('factsByIds пропускает неизвестные идентификаторы', () => {
    const facts = [fact('f1'), fact('f2')]
    expect(factsByIds(facts, ['f2', 'zzz', 'f1']).map((f) => f.id)).toEqual(['f2', 'f1'])
    expect(factsByIds(facts, undefined)).toEqual([])
  })

  it('confidenceLevel: неизвестное значение — низкая уверенность, «установлено» не существует', () => {
    expect(confidenceLevel('high')).toBe('high')
    expect(confidenceLevel('medium')).toBe('medium')
    for (const v of ['certain', 'established', '', undefined]) expect(confidenceLevel(v)).toBe('low')
  })

  it('parseNamespaces: строки и запятые, без пустых и повторов', () => {
    expect(parseNamespaces(' kube-system\n\nvault, vault \n')).toEqual(['kube-system', 'vault'])
    expect(parseNamespaces('')).toEqual([])
  })

  it('validateSettings: границы 1–15 и 1–365', () => {
    const ok: IncidentSettings = { disableLogEvidence: false, excludedNamespaces: [], fragmentRetentionDays: 15, incidentRetentionDays: 90 }
    expect(validateSettings(ok, 15, 365)).toBe('')
    expect(validateSettings({ ...ok, fragmentRetentionDays: 16 }, 15, 365)).toBe('fragmentDays')
    expect(validateSettings({ ...ok, fragmentRetentionDays: 0 }, 15, 365)).toBe('fragmentDays')
    expect(validateSettings({ ...ok, fragmentRetentionDays: 1.5 }, 15, 365)).toBe('fragmentDays')
    expect(validateSettings({ ...ok, incidentRetentionDays: 366 }, 15, 365)).toBe('incidentDays')
    expect(validateSettings({ ...ok, incidentRetentionDays: Number.NaN }, 15, 365)).toBe('incidentDays')
  })

  it('normalizeIncident: null-срезы Go превращаются в пустые массивы', () => {
    const raw = incident() as unknown as Record<string, unknown>
    raw.events = null
    raw.evidence = null
    const report = raw.report as Record<string, unknown>
    report.facts = null
    report.hypotheses = [{ id: 'h1', supporting: null, contradicting: null }]
    const n = normalizeIncident(raw as unknown as Incident)
    expect(n.events).toEqual([])
    expect(n.evidence).toEqual([])
    expect(n.report.facts).toEqual([])
    expect(n.report.hypotheses[0].supporting).toEqual([])
    expect(n.report.hypotheses[0].contradicting).toEqual([])
  })
})
