// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../../i18n'
import en from '../../../i18n/locales/en/incidentsMap.json'
import ru from '../../../i18n/locales/ru/incidentsMap.json'
import cn from '../../../i18n/locales/cn/incidentsMap.json'
import ingress from '../__fixtures__/map/ingress.json'
import configmap from '../__fixtures__/map/configmap.json'
import truncated from '../__fixtures__/map/truncated.json'
import legacy from '../__fixtures__/map/legacy-no-graph.json'
import emptyGraph from '../__fixtures__/map/empty-graph.json'
import DependencyMap from './DependencyMap.vue'
import MapDetails from './MapDetails.vue'
import { buildMapModel } from '../../../composables/dependencyMap'
import type { Report } from '../../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))
const rep = (r: unknown) => r as Report
const count = (html: string, needle: string) => html.split(needle).length - 1

describe('DependencyMap', () => {
  it('рисует вершины и рёбра фикстуры; кандидат и цепочка помечены', async () => {
    const html = await render(DependencyMap, { report: rep(ingress) })
    expect(count(html, 'data-testid="map-node"')).toBe(7)
    expect(count(html, 'data-testid="map-edge"')).toBe(6)
    expect(html).toContain('data-candidate="true"')
    expect(html).toContain('data-chain="true"')
    expect(html).toContain('data-testid="map-legend"')
    expect(html).not.toContain('incidentsMap.') // нет «сырых» ключей перевода
  })

  it('заглушка, Unknown и Secret различимы атрибутом варианта и подписью, не только цветом', async () => {
    const html = await render(DependencyMap, { report: rep(configmap) })
    expect(html).toContain('data-variant="placeholder"')
    expect(html).toContain('data-variant="secret"')
    expect(html).toContain('data-health="Missing"')
    expect(html).toMatch(/data-state="missing"/)
    expect(html).toMatch(/data-state="unknown"/)
  })

  it('роли и клавиатура: узлы — кнопки, ровно один в порядке табуляции, есть aria-метка карты', async () => {
    const html = await render(DependencyMap, { report: rep(ingress) })
    expect(count(html, 'role="button"')).toBe(7)
    expect(count(html, 'tabindex="0"')).toBe(1)
    expect(html).toContain('aria-pressed="false"')
    expect(html).toContain('role="group"')
  })

  it('выбранный ресурс (ресурс или ключ) подсвечен и открывает панель деталей', async () => {
    const byRef = await render(DependencyMap, { report: rep(ingress), selected: { group: 'networking.k8s.io', kind: 'Ingress', namespace: 'shop', name: 'web' } })
    const byKey = await render(DependencyMap, { report: rep(ingress), selected: 'networking.k8s.io/Ingress/shop/web' })
    for (const html of [byRef, byKey]) {
      expect(html).toContain('aria-pressed="true"')
      expect(html).toContain('data-testid="map-facts"')
      expect(html).toContain('data-testid="map-hypotheses"')
      expect(html).toContain('data-testid="map-relations"')
    }
  })

  it('граф неполный: баннер для усечённого графа, неизвестные вид и состояние не ломают отрисовку', async () => {
    const html = await render(DependencyMap, { report: rep(truncated) })
    expect(html).toContain('data-testid="map-incomplete"')
    expect(count(html, 'data-testid="map-node"')).toBe(4)
    expect(html).toContain('data-health="other"')
    expect((await render(DependencyMap, { report: rep(ingress) }))).not.toContain('data-testid="map-incomplete"')
  })

  it('нет графа (старый отчёт), пустые срезы, null — карта скрыта без ошибки', async () => {
    for (const report of [rep(legacy), rep(emptyGraph), null, undefined]) {
      const html = await render(DependencyMap, { report })
      expect(html).not.toContain('data-testid="dependency-map"')
    }
  })
})

describe('MapDetails', () => {
  it('Secret — пояснение «не читается», это не «нет такого»; заглушка — «не существует»', async () => {
    const m = buildMapModel(rep(configmap))
    const secret = await render(MapDetails, { report: rep(configmap), model: m, node: m.nodes.find((n) => n.kind === 'Secret') })
    expect(secret).toContain('data-testid="map-details-secret"')
    expect(secret).not.toContain('data-testid="map-details-placeholder"')
    const ph = await render(MapDetails, { report: rep(configmap), model: m, node: m.nodes.find((n) => n.name === 'app-config') })
    expect(ph).toContain('data-testid="map-details-placeholder"')
  })

  it('Unknown показывает причину словами; пустой выбор — подсказка', async () => {
    const m = buildMapModel(rep(truncated))
    const gadget = await render(MapDetails, { report: rep(truncated), model: m, node: m.nodes.find((n) => n.kind === 'Gadget') })
    expect(gadget).toContain('data-testid="map-details-unknown"')
    expect(gadget).not.toContain('mystery') // неизвестная причина «сырой» не выводится
    expect(await render(MapDetails, { report: rep(truncated), model: m, node: null })).toContain('data-testid="map-details-empty"')
  })

  it('свёрнутая группа даёт кнопку раскрытия', async () => {
    const m = buildMapModel(rep(ingress))
    const group = { ...m.nodes[0], key: 'group:Pod', variant: 'group' as const, members: ['a', 'b', 'c'] }
    expect(await render(MapDetails, { report: rep(ingress), model: m, node: group })).toContain('data-testid="map-expand"')
  })
})

describe('переводы incidentsMap', () => {
  const flat = (o: Record<string, unknown>, p = ''): Record<string, string> =>
    Object.entries(o).reduce<Record<string, string>>((acc, [k, v]) => (typeof v === 'object' && v ? { ...acc, ...flat(v as Record<string, unknown>, `${p}${k}.`) } : { ...acc, [`${p}${k}`]: String(v) }), {})
  const placeholders = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join(',')

  it('en, ru и cn совпадают по ключам и плейсхолдерам', () => {
    const base = flat(en)
    for (const other of [flat(ru), flat(cn)]) {
      expect(Object.keys(other).sort()).toEqual(Object.keys(base).sort())
      for (const k of Object.keys(base)) expect(placeholders(other[k]), k).toBe(placeholders(base[k]))
    }
  })
})
