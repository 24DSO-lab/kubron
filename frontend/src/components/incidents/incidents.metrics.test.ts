// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import { formatBytes, formatLimit, formatMetricValue, formatWindow, isMetricFact, metricLine } from '../../composables/metricFormat'
import FindingsPanel from './FindingsPanel.vue'
import MetricFacts from './MetricFacts.vue'
import MonitoringHint from './MonitoringHint.vue'
import { chainIncident, legacyIncident, metricsIncident, noMetricsIncident } from './__fixtures__'
import type { Fact, MissingContext } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const count = (html: string, needle: string) => html.split(needle).length - 1
const pod = { kind: 'Pod', namespace: 'shop', name: 'api-1' }
const miss = (reason?: string, code = 'metrics.unavailable'): MissingContext => ({ code, resource: pod, params: reason ? { reason } : undefined })

describe('форматирование метрик', () => {
  it('значение по единице: доля — проценты, счётчик — целое, флаг — словами, мусор — «—»', () => {
    expect(formatMetricValue('ratio', '0.970')).toBe('97%')
    expect(formatMetricValue('count', '7.000')).toBe('7')
    expect(formatMetricValue('flag', '1')).toBe('pressure seen')
    expect(formatMetricValue('flag', '0')).toBe('no pressure')
    expect(formatMetricValue('ratio', 'abc')).toBe('—')
    expect(formatMetricValue('ratio', undefined)).toBe('—')
    expect(formatMetricValue('weird', '1.5')).toBe('1.5')
  })
  it('байты, окно и лимит', () => {
    expect(formatBytes(268435456)).toBe('256 MiB')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatWindow('1h')).toBe('1 hour')
    expect(formatWindow('15m')).toBe('15 minutes')
    expect(formatWindow('')).toBe('')
    expect(formatLimit('memory_usage_vs_limit', '268435456.000')).toBe('256 MiB')
    expect(formatLimit('cpu_usage_vs_limit', '0.500')).toBe('0.5 cores')
    expect(formatLimit('container_restarts', '5')).toBe('')
  })
  it('строка факта: значение, агрегат, окно, лимит; без окна и неизвестный агрегат', () => {
    const f = metricsIncident.report.facts.find((x) => x.code === 'metric.memory_usage_vs_limit')!
    expect(metricLine(f).text).toBe('97% · peak over 1 hour · limit 256 MiB')
    const disk: Fact = { ...f, code: 'metric.node_disk_available_ratio', params: { signal: 'node_disk_available_ratio', value: '0.420', unit: 'ratio', agg: 'last' } }
    expect(metricLine(disk).text).toBe('42% · latest value')
    const odd: Fact = { ...f, params: { unit: 'ratio', value: '0.1', agg: 'strange', window: '1h' } }
    expect(metricLine(odd).text).not.toContain('strange')
  })
  it('isMetricFact различает метрики и обычные факты', () => {
    expect(metricsIncident.report.facts.map(isMetricFact)).toEqual([false, true, true])
  })
})

describe('MetricFacts', () => {
  it('рисует только метрические факты', async () => {
    const html = await render(MetricFacts, { facts: metricsIncident.report.facts })
    expect(count(html, 'data-testid="metric-fact"')).toBe(2)
    expect(html).toContain('97%')
    expect(html).toContain('4%')
  })
  it('без метрик ничего не рисует', async () => {
    expect(await render(MetricFacts, { facts: legacyIncident.report.facts })).not.toContain('metric-facts')
    expect(await render(MetricFacts, {})).not.toContain('metric-facts')
  })
})

describe('MonitoringHint', () => {
  it('появляется только при MissingContext metrics.*', async () => {
    expect(await render(MonitoringHint, { missing: [] })).not.toContain('monitoring-hint')
    expect(await render(MonitoringHint, {})).not.toContain('monitoring-hint')
    expect(await render(MonitoringHint, { missing: [{ code: 'graph.unavailable', resource: pod, params: { reason: 'forbidden' } }] })).not.toContain('monitoring-hint')
  })
  it('not_installed — подсказка и кнопка перехода в настройки кластера', async () => {
    const html = await render(MonitoringHint, { missing: [miss('not_installed')] })
    expect(html).toContain('data-testid="monitoring-hint"')
    expect(html).toContain('not installed')
    expect(html).toContain('data-testid="monitoring-open-settings"')
  })
  it('forbidden — пояснение про роль, без кнопки установки', async () => {
    const html = await render(MonitoringHint, { missing: [miss('forbidden')] })
    expect(html).toContain('prometheus-proxy-access')
    expect(html).not.toContain('monitoring-open-settings')
  })
  it('остальные причины — нейтрально, неизвестная причина — общая фраза без «сырого» кода', async () => {
    for (const r of ['unavailable', 'family_missing', 'no_series', 'history_short', 'invalid_ref']) {
      const html = await render(MonitoringHint, { missing: [miss(r, 'metrics.signal_unavailable')] })
      expect(html).toContain('data-testid="monitoring-hint"')
      expect(html).not.toContain('monitoring-open-settings')
    }
    const odd = await render(MonitoringHint, { missing: [miss('brand_new_reason')] })
    expect(odd).not.toContain('brand_new_reason')
    expect(odd).toContain('Diagnostics are based on Kubernetes data only')
  })
  it('несколько причин: одинаковые схлопываются, not_installed первым', async () => {
    const html = await render(MonitoringHint, { missing: [miss('no_series', 'metrics.signal_unavailable'), miss('no_series', 'metrics.signal_unavailable'), miss('not_installed')] })
    expect(count(html, 'data-testid="monitoring-hint-text"')).toBe(2)
    expect(html.indexOf('data-reason="not_installed"')).toBeLessThan(html.indexOf('data-reason="no_series"'))
  })
})

describe('FindingsPanel: метрики и подсказка', () => {
  it('отчёт с метриками показывает значения и источник «метрика мониторинга», подсказки нет', async () => {
    const html = await render(FindingsPanel, { incident: metricsIncident })
    expect(count(html, 'data-testid="metric-fact"')).toBe(2)
    expect(html).toContain('Source: Monitoring metric')
    expect(html).not.toContain('monitoring-hint')
  })
  it('отчёт без метрик: подсказка есть, отчёт полный, сырых кодов причин нет', async () => {
    const html = await render(FindingsPanel, { incident: noMetricsIncident })
    expect(count(html, 'data-testid="monitoring-hint"')).toBe(1)
    expect(html).toContain('data-testid="missing-context"')
    for (const raw of ['not_installed', 'family_missing', 'no_series', 'history_short', 'invalid_ref']) {
      expect(html.replace(/data-reason="[^"]*"/g, '')).not.toContain(raw)
    }
  })
  it('инциденты без metrics.* и старый инцидент — без подсказки и без блока метрик', async () => {
    for (const inc of [chainIncident, legacyIncident]) {
      const html = await render(FindingsPanel, { incident: inc })
      expect(html).not.toContain('monitoring-hint')
      expect(html).not.toContain('metric-facts')
    }
  })
})
