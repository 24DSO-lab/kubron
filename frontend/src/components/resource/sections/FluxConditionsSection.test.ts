// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../../i18n'
import type { FluxCondition, FluxObjectInfo } from '../../../types/flux'
import FluxConditionsSection, { conditionTone } from './FluxConditionsSection.vue'

const cond = (p: Partial<FluxCondition>): FluxCondition => ({
  type: 'Ready', status: 'True', lastTransitionTime: '2026-11-07T10:00:00Z', reason: 'ReconciliationSucceeded', message: 'Applied revision', ...p,
})

const render = (resource: Partial<FluxObjectInfo>) =>
  renderToString(createSSRApp({ render: () => h(FluxConditionsSection, { resource }) }).use(i18n))

describe('conditionTone', () => {
  it('Ready: True — норма, False — ошибка, Unknown — нейтрально', () => {
    expect(conditionTone({ type: 'Ready', status: 'True' })).toBe('rd-ok')
    expect(conditionTone({ type: 'Ready', status: 'False' })).toBe('rd-err')
    expect(conditionTone({ type: 'Ready', status: 'Unknown' })).toBe('rd-unk')
  })
  it('Stalled: True — ошибка, False — не красится', () => {
    expect(conditionTone({ type: 'Stalled', status: 'True' })).toBe('rd-err')
    expect(conditionTone({ type: 'Stalled', status: 'False' })).toBe('rd-unk')
  })
  it('Reconciling: True — предупреждение, False — не красится', () => {
    expect(conditionTone({ type: 'Reconciling', status: 'True' })).toBe('rd-warn')
    expect(conditionTone({ type: 'Reconciling', status: 'False' })).toBe('rd-unk')
  })
  it('прочие типы: True — норма, False — ошибка; пустой статус — нейтрально', () => {
    expect(conditionTone({ type: 'Healthy', status: 'True' })).toBe('rd-ok')
    expect(conditionTone({ type: 'Healthy', status: 'False' })).toBe('rd-err')
    expect(conditionTone({ type: 'Healthy', status: '' })).toBe('rd-unk')
  })
})

describe('FluxConditionsSection', () => {
  it('рисует строку на условие с типом, причиной и сообщением', async () => {
    const html = await render({ conditions: [cond({}), cond({ type: 'Reconciling', status: 'False', reason: 'Succeeded', message: 'idle' })] })
    expect(html.match(/data-testid="flux-condition-row"/g)).toHaveLength(2)
    expect(html).toContain('ReconciliationSucceeded')
    expect(html).toContain('Applied revision')
    expect(html).toContain('Reconciling')
  })

  it('пустой список и отсутствующее поле — пустое состояние', async () => {
    expect(await render({ conditions: [] })).toContain('data-testid="flux-conditions-empty"')
    expect(await render({})).toContain('data-testid="flux-conditions-empty"')
  })

  it('некорректное время не даёт «Invalid Date»', async () => {
    const html = await render({ conditions: [cond({ lastTransitionTime: 'вчера' }), cond({ lastTransitionTime: '' })] })
    expect(html).not.toContain('Invalid')
  })

  it('пустые причина, сообщение и статус заменяются прочерком и «неизвестно»', async () => {
    const html = await render({ conditions: [cond({ reason: '', message: '', status: '' })] })
    expect(html).not.toContain('flux.conditions')
    expect(html).toContain('data-testid="flux-condition-row"')
  })
})
