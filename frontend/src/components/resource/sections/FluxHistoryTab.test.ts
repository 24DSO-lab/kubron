// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../../i18n'
import type { FluxHelmHistoryEntry, FluxHelmReleaseInfo } from '../../../types/flux'
import FluxHistoryTab from './FluxHistoryTab.vue'
import { sortHistory } from './fluxHistory'

const entry = (version: number, p: Partial<FluxHelmHistoryEntry> = {}): FluxHelmHistoryEntry => ({
  version, status: 'deployed', chartName: 'podinfo', chartVersion: '6.5.0', appVersion: '6.5.0',
  firstDeployed: '2026-11-01T10:00:00Z', lastDeployed: '2026-11-02T10:00:00Z', digest: '', ...p,
})
const render = (resource: Partial<FluxHelmReleaseInfo>) =>
  renderToString(createSSRApp({ render: () => h(FluxHistoryTab, { resource }) }).use(i18n))

describe('sortHistory', () => {
  it('новые версии первыми, вход не мутируется', () => {
    const src = [entry(1), entry(3), entry(2)]
    expect(sortHistory(src).map(e => e.version)).toEqual([3, 2, 1])
    expect(src.map(e => e.version)).toEqual([1, 3, 2])
  })
  it('пусто, null и undefined', () => {
    expect(sortHistory([])).toEqual([])
    expect(sortHistory(null)).toEqual([])
    expect(sortHistory(undefined)).toEqual([])
  })
})

describe('FluxHistoryTab', () => {
  it('строка на запись, порядок — новые первыми', async () => {
    const html = await render({ history: [entry(1), entry(2)] })
    expect(html.match(/data-testid="flux-history-row"/g)).toHaveLength(2)
    expect(html.indexOf('#2')).toBeLessThan(html.indexOf('#1'))
    expect(html).toContain('podinfo@6.5.0')
  })
  it('пустая история и отсутствующее поле — пустое состояние', async () => {
    expect(await render({ history: [] })).toContain('data-testid="flux-history-empty"')
    expect(await render({})).toContain('data-testid="flux-history-empty"')
  })
  it('некорректное время и пустые поля не ломают рендер', async () => {
    const html = await render({ history: [entry(1, { lastDeployed: 'x', chartName: '', chartVersion: '', appVersion: '', status: '' })] })
    expect(html).not.toContain('Invalid')
    expect(html).toContain('data-testid="flux-history-row"')
  })
})
