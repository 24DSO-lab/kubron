// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import EvidencePanel from './EvidencePanel.vue'
import FindingsPanel from './FindingsPanel.vue'
import IncidentSettings from './IncidentSettings.vue'
import type { Fact, FragmentView, Hypothesis, Incident, IncidentSettingsInfo } from '../../types/incident'

const ref = { kind: 'Pod', namespace: 'shop', name: 'api-1' }
const src = { kind: 'resource' as const, resource: ref, field: 'status.phase' }

const logFact: Fact = { kind: 'fact', id: 'f1', code: 'container.log_tail', resource: ref, params: { container: 'app' },
  source: { kind: 'log', resource: ref }, observedAt: '2026-11-01T10:00:00Z', log: { container: 'app', previous: true } }
const restarts: Fact = { kind: 'fact', id: 'f2', code: 'container.restarts', resource: ref, params: { container: 'app', count: '7' },
  source: src, observedAt: '2026-11-01T10:00:00Z' }
const hyp: Hypothesis = { kind: 'hypothesis', id: 'h1', code: 'crash.application_error', resource: ref, params: { container: 'app', exitCode: '1' },
  confidence: 'medium', supporting: ['f2'], contradicting: [] }

function incident(extra: Partial<Incident> = {}): Incident {
  return {
    id: 'i1', target: ref, status: 'open', openedAt: '2026-11-01T10:00:00Z', updatedAt: '', openedBy: 'alice', events: [],
    eventsUpdatedAt: '', saveLogs: true, version: 1,
    evidence: [{ pod: 'api-1', container: 'app', previous: true, status: 'not_saved', reason: 'redaction_failed', updatedAt: '' }],
    report: { ruleSetVersion: 1, target: ref, observedAt: '', symptoms: ['pod.crash_loop'], resources: [ref], facts: [restarts, logFact],
      hypotheses: [hyp], recommendations: [{ kind: 'recommendation', id: 'r1', code: 'rec.fix_application_error', resource: ref, hypotheses: ['h1'] }],
      steps: [{ kind: 'step', id: 's1', code: 'step.read_previous_logs', resource: ref, params: { container: 'app' }, hypotheses: ['h1'] }],
      missing: [{ code: 'logs.unavailable', resource: ref, params: { container: 'app', reason: 'forbidden' } }] },
    ...extra,
  }
}

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))

describe('FindingsPanel', () => {
  it('факт, гипотеза, рекомендация и шаг различимы типом и подписью', async () => {
    const html = await render(FindingsPanel, { incident: incident() })
    for (const kind of ['fact', 'hypothesis', 'recommendation', 'step']) expect(html).toContain(`data-kind="${kind}"`)
    expect(html).toContain('data-testid="hypothesis-note"')
  })

  it('гипотеза показывает уверенность и подтверждающие данные, но не «установленную причину»', async () => {
    const html = await render(FindingsPanel, { incident: incident() })
    expect(html).toContain('data-confidence="medium"')
    expect(html).toContain('data-testid="supporting"')
    expect(html).toContain('data-testid="contradicting"')
    expect(html).not.toContain('hypothesis-confirmed')
  })

  it('подтверждённая человеком гипотеза помечена как подтверждённая человеком', async () => {
    const outcome = { result: 'resolved' as const, confirmedHypothesisId: 'h1', markedBy: 'bob', markedAt: '2026-11-01T11:00:00Z' }
    expect(await render(FindingsPanel, { incident: incident({ outcome }) })).toContain('data-testid="hypothesis-confirmed"')
  })

  it('«доказательство не сохранено» и недостающий контекст показываются явно', async () => {
    const html = await render(FindingsPanel, { incident: incident() })
    expect(html).toContain('data-testid="evidence-not-saved"')
    expect(html).toContain('data-testid="missing-context"')
    expect(html).toContain('data-code="logs.unavailable"')
  })

  it('сохранённое доказательство помечено как сохранённое', async () => {
    const inc = incident({ evidence: [{ pod: 'api-1', container: 'app', previous: true, status: 'saved', fragmentId: 'fr1', updatedAt: '' }] })
    const html = await render(FindingsPanel, { incident: inc })
    expect(html).toContain('data-testid="evidence-saved"')
    expect(html).not.toContain('data-testid="evidence-not-saved"')
  })
})

const meta = (extra: Partial<FragmentView> = {}): FragmentView => ({
  id: 'fr1', incidentId: 'i1', namespace: 'shop', pod: 'api-1', container: 'app', previous: true, capturedAt: '', startLine: 1, endLine: 3,
  anchorLine: 3, lines: 3, bytes: 40, truncated: false, redactorVersion: 1, replacements: 2, sha256: 'x', auto: true, createdAt: '', createdBy: 'alice',
  contentHidden: false, ...extra,
})

describe('EvidencePanel', () => {
  const read = async () => ({ ...meta(), text: 'secret-free text' })

  it('без права diagnostics.logs: метаданные видны, «содержимое скрыто», кнопки показа нет', async () => {
    const html = await render(EvidencePanel, { incident: incident(), fragments: [meta({ contentHidden: true })], read, canDelete: false })
    expect(html).toContain('data-testid="content-hidden"')
    expect(html).toContain('data-testid="fragment-meta"')
    expect(html).not.toContain('data-testid="fragment-show"')
    expect(html).not.toContain('data-testid="fragment-delete"')
    expect(html).not.toContain('secret-free text')
  })

  it('с правом: есть показ и удаление, содержимое не рендерится до явного показа', async () => {
    const html = await render(EvidencePanel, { incident: incident(), fragments: [meta()], read, canDelete: true })
    expect(html).toContain('data-testid="fragment-show"')
    expect(html).toContain('data-testid="fragment-delete"')
    expect(html).not.toContain('data-testid="fragment-content"')
  })

  it('несохранённое доказательство можно сохранить явным действием, если сохранение не отключено', async () => {
    const on = await render(EvidencePanel, { incident: incident(), fragments: [], read, canDelete: false })
    expect(on).toContain('data-testid="evidence-missing"')
    expect(on).not.toMatch(/data-testid="evidence-save"[^>]*disabled/)
    const off = await render(EvidencePanel, { incident: incident({ saveLogs: false }), fragments: [], read, canDelete: false })
    expect(off).toMatch(/disabled[^>]*data-testid="evidence-save"|data-testid="evidence-save"[^>]*disabled/)
  })

  it('заявляет, что фрагменты не передаются во внешний AI', async () => {
    expect(await render(EvidencePanel, { incident: incident(), fragments: [], read, canDelete: false })).toContain('data-testid="never-external"')
  })
})

describe('IncidentSettings', () => {
  const info = (canManage: boolean): IncidentSettingsInfo => ({
    canManage,
    settings: { disableLogEvidence: false, excludedNamespaces: ['vault'], fragmentRetentionDays: 15, incidentRetentionDays: 90 },
    limits: { fragmentLines: 200, fragmentBytes: 32768, lineBytes: 2048, fragmentsPerIncident: 10, incidentFragmentBytes: 262144,
      maxFragmentRetentionDays: 15, maxIncidentRetentionDays: 365 },
  })

  it('администратору доступно сохранение, остальным — только чтение', async () => {
    const admin = await render(IncidentSettings, { info: info(true) })
    expect(admin).toContain('data-testid="settings-save"')
    expect(admin).not.toContain('data-testid="settings-readonly"')
    const viewer = await render(IncidentSettings, { info: info(false) })
    expect(viewer).not.toContain('data-testid="settings-save"')
    expect(viewer).toContain('data-testid="settings-readonly"')
    expect(viewer).toContain('data-testid="limits"')
  })
})
