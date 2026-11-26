// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** D8: все виды объектов в форме открытия, пересчёт корреляции, новые виды кандидата, метрики без падений. */
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import CorrelationBlock from './CorrelationBlock.vue'
import FindingsPanel from './FindingsPanel.vue'
import OpenIncidentForm from './OpenIncidentForm.vue'
import { correlationIncident, metricsIncident } from './__fixtures__'
import { TARGET_KINDS, isClusterScoped } from '../../types/incident'
import type { CorrelationResult, Incident } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const visible = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ')

describe('OpenIncidentForm: виды объектов', () => {
  it('в списке все одиннадцать видов, у каждого есть подпись', async () => {
    const html = await render(OpenIncidentForm, {})
    expect(TARGET_KINDS).toHaveLength(11)
    for (const k of TARGET_KINDS) expect(html).toContain(`value="${k}"`)
  })

  it('Node — кластерный объект: поле namespace не показывается', async () => {
    expect(isClusterScoped('Node')).toBe(true)
    expect(isClusterScoped('Pod')).toBe(false)
    const node = await render(OpenIncidentForm, { initial: { kind: 'Node', name: 'worker-1' } })
    expect(node).not.toContain('data-testid="open-namespace"')
    expect(node).toContain('data-testid="open-name"')
    // имя задано, namespace не нужен — кнопка не заблокирована
    expect(/<button[^>]*data-testid="open-submit"[^>]*>/.exec(node)![0]).not.toContain('disabled')
    const pod = await render(OpenIncidentForm, { initial: { kind: 'Pod', name: 'web-1' } })
    expect(pod).toContain('data-testid="open-namespace"')
    expect(/<button[^>]*data-testid="open-submit"[^>]*>/.exec(pod)![0]).toContain('disabled')
  })
})

const corr = (kind: string, confidence = 'low'): CorrelationResult => ({
  version: 1, observedAt: '2026-11-19T10:00:00Z',
  candidates: [{ id: 'c1', kind, source: { kind: 'Namespace', name: 'shop' }, confidence: confidence as never, total: 2,
    affected: [{ kind: 'Deployment', namespace: 'shop', name: 'a' }, { kind: 'Deployment', namespace: 'shop', name: 'b' }] }],
})

describe('CorrelationBlock: пересчёт и виды', () => {
  it('без статуса кнопки нет (блок только для чтения)', async () => {
    expect(await render(CorrelationBlock, { correlation: corr('shared_node') })).not.toContain('correlation-recompute"')
  })

  it('открытое расследование: кнопка доступна, подпись «Recalculate correlation»', async () => {
    const html = await render(CorrelationBlock, { correlation: corr('shared_node'), status: 'open', canOperate: true })
    const btn = /<button[^>]*data-testid="correlation-recompute"[^>]*>[\s\S]*?<\/button>/.exec(html)![0]
    expect(btn).not.toContain('disabled')
    expect(visible(btn)).toContain('Recalculate correlation')
  })

  it('идёт запрос: кнопка заблокирована и показывает ход', async () => {
    const html = await render(CorrelationBlock, { correlation: corr('shared_node'), status: 'open', canOperate: true, busy: true })
    const btn = /<button[^>]*data-testid="correlation-recompute"[^>]*>[\s\S]*?<\/button>/.exec(html)![0]
    expect(btn).toContain('disabled')
    expect(visible(btn)).toContain('Recalculating')
  })

  it('закрытое расследование и отсутствие права operate: кнопка недоступна, причина названа', async () => {
    const closed = await render(CorrelationBlock, { correlation: corr('shared_node'), status: 'closed', canOperate: true })
    expect(/<button[^>]*data-testid="correlation-recompute"[^>]*>/.exec(closed)![0]).toContain('disabled')
    expect(visible(closed)).toContain('closed investigation is not recalculated')
    const noRight = await render(CorrelationBlock, { correlation: corr('shared_node'), status: 'open', canOperate: false })
    expect(/<button[^>]*data-testid="correlation-recompute"[^>]*>/.exec(noRight)![0]).toContain('disabled')
  })

  it('shared_dependency, shared_root и shared_symptom переведены; для shared_symptom — пометка низкой уверенности', async () => {
    for (const kind of ['shared_dependency', 'shared_root', 'shared_symptom']) {
      const html = await render(CorrelationBlock, { correlation: corr(kind) })
      expect(visible(html), kind).not.toMatch(new RegExp(`(?<![\\w.])${kind}(?![\\w.])`))
      expect(html).toContain(`data-kind="${kind}"`)
      expect(visible(html)).toContain('Possible common source')
    }
    const symptom = await render(CorrelationBlock, { correlation: corr('shared_symptom') })
    expect(symptom).toContain('data-testid="correlation-symptom-hint"')
    expect(await render(CorrelationBlock, { correlation: corr('shared_root') })).not.toContain('correlation-symptom-hint')
  })

  it('неизвестный вид кандидата — нейтральная подпись, а не сырой код', async () => {
    const html = await render(CorrelationBlock, { correlation: corr('shared_future_thing') })
    expect(visible(html)).not.toContain('shared_future_thing')
    expect(visible(html)).toContain('Possible common source')
  })
})

describe('метрики B', () => {
  it('факты metric.* отчёта показываются строками, неизвестный сигнал не роняет экран', async () => {
    const extra: Incident = JSON.parse(JSON.stringify(metricsIncident))
    extra.report.facts.push({ kind: 'fact', id: 'fx', code: 'metric.future_signal', resource: extra.target, params: { value: 'x', unit: 'weird', agg: 'avg', window: '5m' },
      source: { kind: 'metric', resource: extra.target }, observedAt: '2026-11-19T10:00:00Z' })
    const html = await render(FindingsPanel, { incident: extra })
    expect(html).toContain('data-testid="metric-fact"')
    expect(visible(html)).not.toContain('metric.future_signal')
  })

  it('инцидент с корреляцией отрисовывается вместе с блоком корреляции', async () => {
    expect(await render(CorrelationBlock, { correlation: correlationIncident.correlation, status: 'open' })).toContain('data-testid="correlation-block"')
  })
})
