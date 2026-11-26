// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import FindingsPanel from './FindingsPanel.vue'
import OutcomeForm from './OutcomeForm.vue'
import { chainIncident, correlationIncident, legacyIncident, metricsIncident } from './__fixtures__'
import type { Incident, Outcome } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const count = (html: string, needle: string) => html.split(needle).length - 1
const withOutcome = (i: Incident, o: Partial<Outcome>): Incident => ({ ...i, outcome: { result: 'resolved', markedBy: 'bob', markedAt: '2026-11-19T11:00:00Z', ...o } })

describe('OutcomeForm', () => {
  it('старый инцидент: обязателен только результат и необязательные поля без пугающих полей', async () => {
    const html = await render(OutcomeForm, { incident: legacyIncident })
    expect(html).toContain('data-testid="outcome-result"')
    expect(html).toContain('data-testid="outcome-optional-hint"')
    expect(html).toContain('Nothing here is required')
    // нет рекомендаций, корреляции и отвергаемых гипотез (одна гипотеза, но и она есть в списке)
    expect(html).not.toContain('outcome-recommendations')
    expect(html).not.toContain('outcome-correlation')
    expect(html).toContain('data-testid="outcome-submit"')
    // ошибки проверки до отправки не показываются, пока человек ничего не отправил
    expect(html).not.toContain('outcome-error')
  })

  it('рекомендации: три состояния, «не отмечено» по умолчанию', async () => {
    const html = await render(OutcomeForm, { incident: chainIncident })
    expect(count(html, 'data-testid="outcome-rec"')).toBe(1)
    expect(html).toContain('data-testid="outcome-rec-none"')
    expect(html).toContain('data-testid="outcome-rec-helpful"')
    expect(html).toContain('data-testid="outcome-rec-useless"')
    const none = /<input[^>]*data-testid="outcome-rec-none"[^>]*>/.exec(html)![0]
    expect(none).toContain('checked')
    expect(/<input[^>]*data-testid="outcome-rec-helpful"[^>]*>/.exec(html)![0]).not.toContain('checked')
  })

  it('«помогло другое» и «когда устранено» — для устранённой проблемы, с границами дат', async () => {
    const html = await render(OutcomeForm, { incident: chainIncident })
    expect(html).toContain('data-testid="outcome-other-action"')
    const field = /<input[^>]*data-testid="outcome-resolved-at"[^>]*>/.exec(html)![0]
    expect(field).toContain('type="datetime-local"')
    expect(field).toContain('min="')
    expect(field).toContain('max="')
  })

  it('оценка кандидата корреляции — только если кандидат есть; четыре состояния', async () => {
    expect(await render(OutcomeForm, { incident: metricsIncident })).not.toContain('outcome-correlation')
    const html = await render(OutcomeForm, { incident: correlationIncident })
    expect(html).toContain('data-testid="outcome-correlation"')
    for (const v of ['none', 'confirmed', 'rejected', 'unknown']) expect(html).toContain(`data-testid="outcome-verdict-${v}"`)
    expect(/<input[^>]*data-testid="outcome-verdict-none"[^>]*>/.exec(html)![0]).toContain('checked')
  })

  it('есть список гипотез для отвергнутых; на старом инциденте — одна гипотеза', async () => {
    expect(count(await render(OutcomeForm, { incident: chainIncident }), 'data-testid="outcome-reject"')).toBe(2)
    expect(count(await render(OutcomeForm, { incident: legacyIncident }), 'data-testid="outcome-reject"')).toBe(1)
  })

  it('сохранённая отметка: сводка того, что человек отметил, словами', async () => {
    const inc = withOutcome(chainIncident, {
      confirmedHypothesisId: 'h2', rejectedHypothesisIds: ['h1'], helpfulRecommendationIds: ['r1'], otherAction: true,
      resolvedAt: '2026-11-19T10:30:00Z', correlation: { candidateId: 'c1', verdict: 'confirmed' },
    })
    const html = await render(OutcomeForm, { incident: inc })
    expect(html).toContain('data-testid="outcome-done"')
    expect(html).not.toContain('data-testid="outcome-submit"')
    expect(html).toContain('Ruled out:')
    expect(html).toContain('Helped:')
    expect(html).toContain('Something outside the recommendations helped')
    expect(html).toContain('Resolved at')
    expect(html).toContain('Possible common source: confirmed')
  })

  it('старая отметка без новых полей выглядит как раньше, без сводки', async () => {
    const html = await render(OutcomeForm, { incident: withOutcome(legacyIncident, { note: 'готово' }) })
    expect(html).toContain('data-testid="outcome-done"')
    expect(html).not.toContain('outcome-marks')
    expect(html).toContain('готово')
  })
})

describe('FindingsPanel: метки отметки человека', () => {
  it('отвергнутая гипотеза и помогло/не помогло видны; рекомендации не скрыты', async () => {
    const inc = withOutcome(chainIncident, { rejectedHypothesisIds: ['h1'], uselessRecommendationIds: ['r1'] })
    const html = await render(FindingsPanel, { incident: inc })
    expect(html).toContain('data-testid="hypothesis-rejected"')
    expect(html).toContain('data-testid="recommendation-useless"')
    expect(count(html, 'data-testid="recommendation"')).toBe(1)
    const helpful = await render(FindingsPanel, { incident: withOutcome(chainIncident, { helpfulRecommendationIds: ['r1'] }) })
    expect(helpful).toContain('data-testid="recommendation-helpful"')
  })

  it('без отметки меток нет', async () => {
    const html = await render(FindingsPanel, { incident: chainIncident })
    expect(html).not.toContain('hypothesis-rejected')
    expect(html).not.toContain('recommendation-helpful')
    expect(html).not.toContain('recommendation-useless')
  })
})
