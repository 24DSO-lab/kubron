// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import FindingsPanel from './FindingsPanel.vue'
import IncidentDetail from './IncidentDetail.vue'
import RecurrenceBanner from './RecurrenceBanner.vue'
import RuleStatsPanel from './RuleStatsPanel.vue'
import { chainIncident, legacyIncident, metricsIncident, ruleStatsFixture } from './__fixtures__'
import type { Incident, RuleStats } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const count = (html: string, needle: string) => html.split(needle).length - 1

describe('FindingsPanel: статистика правил', () => {
  it('доверенная статистика гипотезы: «в N из M», без процентов в пометке', async () => {
    const html = await render(FindingsPanel, { incident: metricsIncident, stats: ruleStatsFixture })
    expect(html).toContain('confirmed in 6 of 8 cases here')
    expect(html).toContain('30 minutes')
    expect(html).not.toMatch(/data-testid="hypothesis-stat"[^<]*%/)
  })

  it('мало случаев: только число, без процента', async () => {
    const html = await render(FindingsPanel, { incident: chainIncident, stats: ruleStatsFixture })
    expect(html).toContain('2 cases so far: too few for a percentage')
    expect(html).not.toContain('confirmed in 1 of 2')
  })

  it('рекомендации остаются на экране; «ранее не помогала» — предупреждение, а не скрытие', async () => {
    const stats: RuleStats = { ...ruleStatsFixture, recommendations: [{ ruleId: 'storage.class_missing', epoch: 1, code: 'rec.create_storageclass', helpful: 0, useless: 4, ratio: 0, sample: 4, trusted: true, previouslyUnhelpful: true }] }
    const html = await render(FindingsPanel, { incident: chainIncident, stats })
    expect(count(html, 'data-testid="recommendation"')).toBe(1)
    expect(html).toContain('previously did not help here (helped in 0 of 4)')
    expect(html).toContain('data-testid="recommendation-stat"')
  })

  it('уровень уверенности не меняется статистикой', async () => {
    const plain = await render(FindingsPanel, { incident: metricsIncident })
    const withStats = await render(FindingsPanel, { incident: metricsIncident, stats: ruleStatsFixture })
    expect(plain).toContain('data-confidence="medium"')
    expect(withStats).toContain('data-confidence="medium"')
    expect(count(withStats, 'data-testid="hypothesis"')).toBe(count(plain, 'data-testid="hypothesis"'))
  })

  it('без статистики и для отчёта без ruleId пометок нет', async () => {
    for (const html of [await render(FindingsPanel, { incident: chainIncident }), await render(FindingsPanel, { incident: legacyIncident, stats: ruleStatsFixture })]) {
      expect(html).not.toContain('hypothesis-stat')
      expect(html).not.toContain('recommendation-stat')
    }
  })
})

describe('RecurrenceBanner', () => {
  const again = (extra: Partial<Incident> = {}): Incident => ({ ...chainIncident, recurrenceOf: 'old', ...extra })
  const earlier: Incident = {
    ...metricsIncident, id: 'old',
    outcome: { result: 'resolved', confirmedHypothesisId: 'h1', helpfulRecommendationIds: [], markedBy: 'bob', markedAt: '2026-11-10T10:00:00Z', resolvedAt: '2026-11-10T09:30:00Z' },
  }

  it('не повторение — нет баннера', async () => {
    expect(await render(RecurrenceBanner, { incident: chainIncident })).not.toContain('recurrence-banner')
  })
  it('повторение с доступным предыдущим: что было, подтверждённая причина и ссылка', async () => {
    const html = await render(RecurrenceBanner, { incident: again(), previous: earlier })
    expect(html).toContain('This looks like a repeat')
    expect(html).toContain('data-testid="recurrence-summary"')
    expect(html).toContain('Confirmed cause then:')
    expect(html).toContain('data-testid="recurrence-open"')
    expect(html).not.toContain('recurrence-helped')
  })
  it('что помогло — только если это было отмечено', async () => {
    const helped: Incident = { ...earlier, report: { ...chainIncident.report }, outcome: { ...earlier.outcome!, confirmedHypothesisId: 'h2', helpfulRecommendationIds: ['r1'] } }
    const html = await render(RecurrenceBanner, { incident: again(), previous: helped })
    expect(html).toContain('data-testid="recurrence-helped"')
    expect(html).toContain('What helped then:')
  })
  it('предыдущее недоступно: баннер без подробностей и без ссылки', async () => {
    for (const previous of [null, undefined]) {
      const html = await render(RecurrenceBanner, { incident: again(), previous })
      expect(html).toContain('data-testid="recurrence-unavailable"')
      expect(html).not.toContain('recurrence-open')
    }
  })
  it('в расследовании показывается над находками', async () => {
    const props = { incident: again(), fragments: [], canReadLogs: false, busy: false, read: async () => ({}), previous: earlier, stats: ruleStatsFixture }
    const html = await render(IncidentDetail, props)
    expect(html.indexOf('recurrence-banner')).toBeGreaterThan(-1)
    expect(html.indexOf('recurrence-banner')).toBeLessThan(html.indexOf('data-testid="findings"'))
  })
})

describe('RuleStatsPanel', () => {
  it('таблицы: гипотезы, рекомендации, симптомы, корреляции; доля только у доверенных', async () => {
    const html = await render(RuleStatsPanel, { stats: ruleStatsFixture })
    expect(count(html, 'data-testid="stats-hypothesis"')).toBe(2)
    expect(count(html, 'data-testid="stats-recommendation"')).toBe(2)
    expect(count(html, 'data-testid="stats-symptom"')).toBe(1)
    expect(count(html, 'data-testid="stats-correlation"')).toBe(1)
    expect(html).toContain('75%')
    // недоверенные: 2 случая и 4 случая — прочерк, процента нет
    const untrusted = /<tr[^>]*data-trusted="false"[^>]*>[\s\S]*?<\/tr>/g
    for (const row of html.match(untrusted) ?? []) expect(row).not.toContain('%')
  })
  it('границы честно названы: сколько учтено, что не учтено, порог в 5 случаев', async () => {
    const html = await render(RuleStatsPanel, { stats: ruleStatsFixture })
    expect(html).toContain('12 closed investigations')
    expect(html).toContain('3 without one are not counted')
    expect(html).toContain('at least 5 cases')
  })
  it('усечение и «пока мало данных»', async () => {
    expect(await render(RuleStatsPanel, { stats: { ...ruleStatsFixture, truncated: true } })).toContain('data-testid="stats-truncated"')
    const empty = { ...ruleStatsFixture, hypotheses: [], recommendations: [], symptoms: [], correlations: [] }
    expect(await render(RuleStatsPanel, { stats: empty })).toContain('data-testid="stats-nothing"')
  })
  it('нет статистики — понятное сообщение', async () => {
    expect(await render(RuleStatsPanel, {})).toContain('data-testid="stats-empty"')
    expect(await render(RuleStatsPanel, { loading: true })).toContain('Loading')
  })
  it('выгрузка — только с правом, с описанием того, что входит и что нет', async () => {
    expect(await render(RuleStatsPanel, { stats: ruleStatsFixture })).not.toContain('stats-export')
    const html = await render(RuleStatsPanel, { stats: ruleStatsFixture, canExport: true })
    expect(html).toContain('data-testid="stats-export-button"')
    expect(html).toContain('no resource names')
    expect(html).toContain('Nothing is sent anywhere')
  })
})
