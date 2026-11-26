// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Сверка с реальными отчётами метрик D1. Фикстуры в `__fixtures__/d1/` получены штатными хелперами тестов
 * `internal/diagnose/metrics_test.go` (metricsPod, analyzeWith, signals.NewSet) и выгружены `json.Marshal(Report)`:
 * это то, что отдаёт бэкенд, а не рукописные данные. Перегенерация: временный Go-тест в package diagnose,
 * который строит те же отчёты и пишет их в JSON.
 */
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import FindingsPanel from './FindingsPanel.vue'
import MonitoringHint from './MonitoringHint.vue'
import { normalizeIncident } from '../../composables/incidentModel'
import memoryNearLimit from './__fixtures__/d1/memory_near_limit.json'
import crashCpuNode from './__fixtures__/d1/crash_cpu_node.json'
import gaps from './__fixtures__/d1/gaps.json'
import type { Incident, MissingContext, Report } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const visible = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ')
const rawCode = (text: string, code: string) => new RegExp(`(?<![\\w.])${code.replaceAll('.', '\\.')}(?![\\w.])`).test(text)

const wrap = (report: Report): Incident => normalizeIncident({
  id: 'd1', target: report.target, status: 'open', openedAt: report.observedAt, updatedAt: report.observedAt, openedBy: 'd1',
  report, events: [], eventsUpdatedAt: report.observedAt, saveLogs: false, evidence: [], version: 1,
})

const reports: Record<string, Report> = {
  memory_near_limit: memoryNearLimit as unknown as Report,
  crash_cpu_node: crashCpuNode as unknown as Report,
}

describe('реальные отчёты метрик D1', () => {
  for (const [name, report] of Object.entries(reports)) {
    it(`${name}: записи переведены, сырых кодов нет, подсказки о мониторинге нет`, async () => {
      const html = await render(FindingsPanel, { incident: wrap(report) })
      const text = visible(html)
      expect(text).not.toContain('Entry not recognised')
      const codes = [...report.symptoms, ...report.facts.map((f) => f.code), ...report.hypotheses.map((x) => x.code),
        ...report.recommendations.map((r) => r.code), ...report.missing.map((m) => m.code)]
      for (const code of codes.filter((c) => c.includes('.'))) expect(rawCode(text, code), `код «${code}» показан сырым`).toBe(false)
      // Метрики есть — подсказка про мониторинг не нужна.
      expect(html).not.toContain('monitoring-hint')
      // Все факты metric.* показаны строкой со значением.
      const metricFacts = report.facts.filter((f) => f.code.startsWith('metric.'))
      expect((html.match(/data-testid="metric-fact"/g) ?? []).length).toBe(metricFacts.length)
      expect(metricFacts.every((f) => f.source.kind === 'metric')).toBe(true)
    })
  }

  it('память у лимита: доля, счётчик OOM и окно словами', async () => {
    const text = visible(await render(FindingsPanel, { incident: wrap(reports.memory_near_limit) }))
    expect(text).toContain('97% · peak over 1 hour')
    expect(text).toContain('2 · peak over 1 hour')
  })

  it('перезапуски, троттлинг CPU и давление на узле: единицы ratio, count и flag', async () => {
    const text = visible(await render(FindingsPanel, { incident: wrap(reports.crash_cpu_node) }))
    expect(text).toContain('60% · peak over 1 hour')
    expect(text).toContain('95% · peak over 1 hour')
    expect(text).toContain('8 · peak over 1 hour')
    expect(text).toContain('pressure seen · peak over 1 hour')
  })
})

describe('реальные пробелы метрик D1', () => {
  const cases = gaps as unknown as Record<string, MissingContext[]>

  it('есть все причины из signals.Reason', () => {
    const reasons = new Set(Object.values(cases).flat().map((m) => m.params?.reason))
    for (const r of ['not_installed', 'forbidden', 'unavailable', 'family_missing', 'no_series', 'history_short', 'invalid_ref']) expect(reasons.has(r), r).toBe(true)
  })

  for (const [name, missing] of Object.entries(cases)) {
    it(`${name}: подсказка о мониторинге есть, причина словами, отчёт не пустой`, async () => {
      const html = await render(MonitoringHint, { missing })
      const text = visible(html)
      expect(html).toContain('data-testid="monitoring-hint"')
      for (const m of missing) {
        const reason = m.params?.reason ?? ''
        expect(rawCode(text, reason), `причина «${reason}» показана сырой`).toBe(false)
      }
      // Кнопка перехода в настройки — только когда стека нет совсем.
      expect(html.includes('monitoring-open-settings')).toBe(name === 'missing_not_installed')
    })
  }
})
