// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import CorrelationBlock from './CorrelationBlock.vue'
import IncidentDetail from './IncidentDetail.vue'
import ProblemList from './ProblemList.vue'
import ProblemView from './ProblemView.vue'
import { correlationIncident, legacyIncident, problemDetail } from './__fixtures__'
import type { CorrelationResult, IncidentSummary, ProblemDetail, ProblemSummary } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const count = (html: string, needle: string) => html.split(needle).length - 1
const correlationResult: CorrelationResult = correlationIncident.correlation!

const summary = (id: string, over: Partial<ProblemSummary> = {}): ProblemSummary => ({
  id, key: 'k', kind: 'shared_node', source: { kind: 'Node', name: 'n1' }, status: 'open', origin: 'auto', memberCount: 2, affectedTotal: 12, updatedAt: '2026-11-19T10:00:00Z', ...over,
})
const inc = (id: string): IncidentSummary => ({ id, target: { kind: 'Pod', namespace: 'shop', name: id }, status: 'open', openedAt: '', updatedAt: '', openedBy: 'a', symptoms: [], facts: 0, hypotheses: 0 })

describe('CorrelationBlock', () => {
  it('показывает источник, уверенность, число и список затронутых, потенциальных отдельно, факты и ссылку на проблему', async () => {
    const html = await render(CorrelationBlock, { correlation: correlationResult, problemId: 'prob-1' })
    expect(html).toContain('data-testid="correlation-block"')
    expect(html).toContain('Possible common source')
    expect(html).toContain('Node/n1')
    expect(html).toContain('Affected workloads: 12')
    expect(count(html, 'data-testid="correlation-workload"')).toBe(3)
    expect(html).toContain('data-testid="correlation-truncated"')
    expect(html).toContain('3 of 12 shown')
    expect(html).toContain('data-testid="correlation-potential"')
    expect(html).toContain('Deployment/shop/cache')
    expect(html).toContain('data-testid="correlation-facts"')
    expect(html).toContain('data-testid="correlation-open-problem"')
  })

  it('формулировки осторожные: «вероятный», не «причина»; нехватка прав названа явно', async () => {
    const html = await render(CorrelationBlock, { correlation: correlationResult })
    expect(html).toContain('likely shared source, not a proven cause')
    expect(html).toContain('no permission to read some data')
    expect(html).not.toContain('data-testid="correlation-open-problem"')
  })

  it('low — явно как подсказка; неизвестный вид без сырого кода', async () => {
    const c: CorrelationResult = { ...correlationResult, candidates: [{ ...correlationResult.candidates![0], confidence: 'low', kind: 'shared_future', truncated: false, total: 3 }] }
    const html = await render(CorrelationBlock, { correlation: c })
    expect(html).toContain('data-testid="correlation-low"')
    expect(html.replace(/data-kind="[^"]*"/g, '')).not.toContain('shared_future')
    expect(html).not.toContain('data-testid="correlation-truncated"')
  })

  it('без корреляции и без кандидатов ничего не рисует', async () => {
    expect(await render(CorrelationBlock, {})).not.toContain('correlation-block')
    expect(await render(CorrelationBlock, { correlation: { version: 1, observedAt: '' } })).not.toContain('correlation-block')
  })
})

describe('IncidentDetail: корреляция', () => {
  const props = (incident: unknown) => ({ incident, fragments: [], canReadLogs: false, busy: false, read: async () => ({}) })
  it('показывает блок корреляции, только если он есть', async () => {
    expect(await render(IncidentDetail, props(correlationIncident))).toContain('data-testid="correlation-block"')
    expect(await render(IncidentDetail, props(legacyIncident))).not.toContain('correlation-block')
  })
})

describe('ProblemList', () => {
  it('строки: источник, участники, затронуто; ручная пометка', async () => {
    const html = await render(ProblemList, { items: [summary('p1'), summary('p2', { origin: 'manual', memberCount: 5 })] })
    expect(count(html, 'data-testid="problem-row"')).toBe(2)
    expect(html).toContain('Node/n1')
    expect(html).toContain('data-testid="problem-manual"')
  })
  it('пусто — сообщение; создание только с правом operate', async () => {
    const empty = await render(ProblemList, { items: [] })
    expect(empty).toContain('data-testid="problem-empty"')
    expect(empty).not.toContain('problem-create')
    const withPerm = await render(ProblemList, { items: [], canOperate: true, candidates: [inc('a'), inc('b')] })
    expect(withPerm).toContain('data-testid="problem-create"')
    expect(count(withPerm, 'data-testid="problem-pick"')).toBe(2)
    expect(withPerm).toMatch(/<button[^>]*disabled[^>]*data-testid="problem-create-submit"/)
  })
})

describe('ProblemView', () => {
  const view = (over: Record<string, unknown> = {}) => render(ProblemView, { problem: problemDetail as ProblemDetail, ...over })
  it('участники со ссылками на расследования, затронутые, усечение и отсоединённые', async () => {
    const html = await view()
    expect(count(html, 'data-testid="problem-member"')).toBe(2)
    expect(count(html, 'data-testid="problem-open-incident"')).toBe(2)
    expect(html).toContain('added automatically')
    expect(html).toContain('data-testid="problem-truncated"')
    expect(html).toContain('data-testid="problem-detached"')
  })
  it('без права operate кнопок действий нет, есть пояснение', async () => {
    const html = await view({ canOperate: false })
    expect(html).not.toContain('problem-detach"')
    expect(html).not.toContain('problem-actions')
    expect(html).toContain('data-testid="problem-readonly"')
  })
  it('с правом operate: отсоединение, присоединение и объединение, с пояснением «ничего не удаляется»', async () => {
    const html = await view({ canOperate: true, attachable: [inc('x')], mergeTargets: [summary('p9')] })
    expect(count(html, 'data-testid="problem-detach"')).toBe(2)
    expect(html).toContain('data-testid="problem-attach"')
    expect(html).toContain('data-testid="problem-merge"')
    expect(html).toContain('not deleted or merged')
    expect(html).not.toContain('problem-readonly')
  })
  it('без проблемы ничего не рисует', async () => {
    expect(await render(ProblemView, {})).not.toContain('problem-view')
  })
})
