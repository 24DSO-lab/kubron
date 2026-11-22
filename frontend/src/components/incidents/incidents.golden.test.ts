// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Сверка интерфейса с золотыми файлами бэкенда: реальные отчёты D3 (цепочки), снимки корреляции D4 и пример статистики D6
 * проходят через компоненты. Проверяется, что контракт не разошёлся: нет «неизвестных записей» и «сырых» кодов,
 * цепочки и корреляция отрисованы, процент не показывается при малой выборке.
 */
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import CorrelationBlock from './CorrelationBlock.vue'
import FindingsPanel from './FindingsPanel.vue'
import RuleStatsPanel from './RuleStatsPanel.vue'
import { normalizeIncident } from '../../composables/incidentModel'
import type { CorrelationResult, Incident, Report, RuleStats } from '../../types/incident'

const root = (p: string) => fileURLToPath(new URL(`../../../../${p}`, import.meta.url))
const read = <T>(p: string): T => JSON.parse(readFileSync(root(p), 'utf8')) as T
const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))

/** Код встречается в тексте как отдельный токен (не внутри слова и не как часть другого кода). */
const rawCode = (text: string, code: string) => new RegExp(`(?<![\\w.])${code.replaceAll('.', '\\.')}(?![\\w.])`).test(text)
/** Видимый текст: без разметки и атрибутов (в атрибутах `data-code` коды остаются намеренно). */
const visible = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ')

const wrap = (report: Report): Incident => normalizeIncident({
  id: 'golden', target: report.target, status: 'open', openedAt: report.observedAt, updatedAt: report.observedAt, openedBy: 'golden',
  report, events: [], eventsUpdatedAt: report.observedAt, saveLogs: false, evidence: [], version: 1,
})

const chainDir = 'internal/diagnose/testdata/chain'
const haveChain = existsSync(root(chainDir))
const haveCorr = existsSync(root('internal/correlation/testdata'))
const haveStats = existsSync(root('internal/feedback/testdata/stats.example.json'))

describe.skipIf(!haveChain)('золотые отчёты D3 (цепочки и хранилище)', () => {
  const cases = haveChain ? readdirSync(root(chainDir), { withFileTypes: true }).filter((d) => d.isDirectory() && existsSync(root(`${chainDir}/${d.name}/report.json`))).map((d) => d.name) : []

  it('есть что проверять', () => expect(cases.length).toBeGreaterThan(0))

  for (const name of cases) {
    it(`${name}: отчёт открывается, записи переведены, сырых кодов нет`, async () => {
      const report = read<Report>(`${chainDir}/${name}/report.json`)
      const html = await render(FindingsPanel, { incident: wrap(report) })
      const text = visible(html)
      expect(text).not.toContain('Entry not recognised')
      // Ни один код (пока он не переведён) не должен просачиваться в видимый текст.
      const codes = [...report.symptoms, ...report.facts.map((f) => f.code), ...report.hypotheses.map((x) => x.code),
        ...report.recommendations.map((r) => r.code), ...report.steps.map((s) => s.code), ...report.missing.map((m) => m.code)]
      // Сырым считается составной код (с точкой): однословные коды вроде «event» совпадают с обычными словами.
      for (const code of codes.filter((c) => c.includes('.'))) expect(rawCode(text, code), `код «${code}» показан сырым`).toBe(false)
      if (report.chains?.length) expect(html).toContain('data-testid="chain-view"')
      else expect(html).not.toContain('data-testid="chain-view"')
    })
  }

  it('Missing и Unknown в золотых цепочках не показываются сырыми кодами', async () => {
    for (const name of cases) {
      const report = read<Report>(`${chainDir}/${name}/report.json`)
      if (!report.chains?.length) continue
      const text = visible(await render(FindingsPanel, { incident: wrap(report) }))
      for (const raw of ['Healthy', 'Degraded', 'Progressing', 'Unknown', 'Missing', 'too_large', 'forbidden']) {
        expect(text, `${name}: «${raw}» показан сырым`).not.toContain(raw)
      }
    }
  })
})

describe.skipIf(!haveCorr)('золотые снимки корреляции D4', () => {
  const dir = 'internal/correlation/testdata'
  const files = haveCorr ? readdirSync(root(dir)).filter((f) => f.endsWith('.golden.json')) : []

  it('есть что проверять', () => expect(files.length).toBeGreaterThan(0))

  for (const f of files) {
    it(`${f}: блок построен, число затронутых и осторожные формулировки на месте`, async () => {
      const c = read<CorrelationResult>(`${dir}/${f}`)
      const html = await render(CorrelationBlock, { correlation: c })
      const text = visible(html)
      expect(html).toContain('data-testid="correlation-block"')
      expect(text).not.toContain('Entry not recognised')
      for (const cand of c.candidates ?? []) {
        expect(text).toContain(`Affected workloads: ${cand.total}`)
        expect(rawCode(text, cand.kind), `вид «${cand.kind}» показан сырым`).toBe(false)
        for (const fact of c.facts ?? []) expect(rawCode(text, fact.code), `факт «${fact.code}» показан сырым`).toBe(false)
      }
      for (const m of c.missing ?? []) expect(rawCode(text, m.code), `«${m.code}» показан сырым`).toBe(false)
      expect(text).toContain('not a proven cause')
    })
  }
})

describe.skipIf(!haveStats)('золотой пример статистики D6', () => {
  const stats = haveStats ? read<RuleStats>('internal/feedback/testdata/stats.example.json') : (null as never)

  it('панель строится из реального примера; доли только у доверенных', async () => {
    const html = await render(RuleStatsPanel, { stats })
    expect(html).toContain('data-testid="stats-hypotheses"')
    expect(html).toContain('data-testid="stats-symptoms"')
    const untrusted = /<tr[^>]*data-trusted="false"[^>]*>[\s\S]*?<\/tr>/g
    for (const row of html.match(untrusted) ?? []) expect(row).not.toContain('%')
    // symptom pod.crash_loop: закрыто 1 — меньше порога, процента нет
    const row = /<tr[^>]*data-testid="stats-symptom"[^>]*>[\s\S]*?<\/tr>/.exec(html)![0]
    expect(row).not.toContain('%')
  })

  it('контракт полей: все поля из примера известны типам интерфейса', () => {
    const known = new Set(['records', 'unevaluated', 'truncated', 'computedAt', 'ruleSetVersions', 'hypotheses', 'recommendations', 'symptoms', 'correlations', 'features'])
    for (const k of Object.keys(stats)) expect(known.has(k), `новое поле статистики «${k}»`).toBe(true)
    const hypKnown = new Set(['ruleId', 'epoch', 'code', 'confirmed', 'rejected', 'precision', 'sample', 'trusted', 'medianResolveSeconds', 'resolved', 'recurred'])
    for (const h of stats.hypotheses) for (const k of Object.keys(h)) expect(hypKnown.has(k), `новое поле гипотезы «${k}»`).toBe(true)
  })
})
