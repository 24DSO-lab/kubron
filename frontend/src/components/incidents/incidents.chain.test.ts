// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import ChainView from './ChainView.vue'
import FindingsPanel from './FindingsPanel.vue'
import { chainIncident, legacyIncident } from './__fixtures__'
import type { Chain } from '../../types/incident'

const render = (component: object, props: Record<string, unknown>) =>
  renderToString(createSSRApp({ render: () => h(component as never, props) }).use(i18n))

const count = (html: string, needle: string) => html.split(needle).length - 1

describe('ChainView', () => {
  it('рисует все цепочки, шаги и выделяет кандидата', async () => {
    const html = await render(ChainView, { chains: chainIncident.report.chains, hypotheses: chainIncident.report.hypotheses })
    expect(count(html, 'data-testid="chain"')).toBe(2)
    expect(count(html, 'data-testid="chain-step"')).toBe(5)
    expect(count(html, 'data-testid="chain-candidate"')).toBe(1)
    expect(html).toContain('PersistentVolumeClaim/data')
  })

  it('Missing и Unknown различимы: другой текст, другая причина, не только цвет', async () => {
    const html = await render(ChainView, { chains: chainIncident.report.chains, hypotheses: chainIncident.report.hypotheses })
    expect(html).toContain('data-health="Missing"')
    expect(html).toContain('data-health="Unknown"')
    expect(html).toContain('does not exist')
    expect(html).toContain('not checked: no permission to read it')
    expect(html).not.toContain('Missing</') // сырой код состояния не показывается
  })

  it('Unknown без причины и неизвестное состояние не дают «сырых» кодов', async () => {
    const chains: Chain[] = [{ path: [
      { ref: { kind: 'Pod', name: 'a' }, health: 'Weird' },
      { ref: { kind: 'Node', name: 'n' }, health: 'Unknown' },
    ] }]
    const html = await render(ChainView, { chains })
    expect(html).not.toContain('Weird')
    expect(html).toContain('state not recognised')
    expect(html).toContain('reason not stated')
  })

  it('доступность: список, фокус и текстовая подпись у шага', async () => {
    const html = await render(ChainView, { chains: chainIncident.report.chains, hypotheses: chainIncident.report.hypotheses })
    expect(html).toContain('<ol')
    expect(html).toContain('tabindex="0"')
    expect(html).toMatch(/aria-label="StorageClass\/fast, missing, does not exist"/)
  })

  it('без цепочек ничего не рисует', async () => {
    expect(await render(ChainView, { chains: [] })).not.toContain('chain-view')
    expect(await render(ChainView, {})).not.toContain('chain-view')
  })
})

describe('FindingsPanel: цепочка и кандидат', () => {
  it('кандидат первым, объяснённая гипотеза под ним, данные не теряются', async () => {
    const html = await render(FindingsPanel, { incident: chainIncident })
    expect(count(html, 'data-testid="hypothesis"')).toBe(2)
    expect(html.indexOf('data-role="candidate"')).toBeLessThan(html.indexOf('data-role="explained"'))
    expect(count(html, 'data-testid="hypothesis-candidate"')).toBe(1)
    expect(count(html, 'data-testid="hypothesis-explained"')).toBe(1)
    expect(count(html, 'data-testid="chain-view"')).toBe(1)
  })

  it('старый инцидент открывается без блока цепочки и без ошибок', async () => {
    const html = await render(FindingsPanel, { incident: legacyIncident })
    expect(html).not.toContain('chain-view')
    expect(html).not.toContain('hypothesis-candidate')
    expect(count(html, 'data-testid="hypothesis"')).toBe(1)
    expect(html).toContain('data-role="plain"')
  })
})
