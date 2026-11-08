// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { i18n } from '../../../i18n'
import type { FluxState } from '../../../types/flux'
import FluxStateBadge from './FluxStateBadge.vue'

const render = (state: string) =>
  renderToString(createSSRApp({ render: () => h(FluxStateBadge, { state: state as FluxState }) }).use(i18n))

// Классы и значения data-state проверяются, а не русский или английский текст: тесты не зависят от языка
const CASES: [FluxState, string][] = [
  ['Ready', 'rd-ok'],
  ['Reconciling', 'rd-warn'],
  ['Suspended', 'rd-warn'],
  ['Failed', 'rd-err'],
  ['Stalled', 'rd-err'],
  ['Unknown', 'rd-unk'],
]

describe('FluxStateBadge', () => {
  it.each(CASES)('%s: класс %s, состояние в data-state, подпись не пустая', async (state, cls) => {
    const html = await render(state)
    expect(html).toContain('data-testid="flux-state-badge"')
    expect(html).toContain(`data-state="${state}"`)
    expect(html).toContain(cls)
    // подпись есть помимо цвета: текст после символа не пустой и это не сырой ключ
    expect(html).not.toContain('flux.state.')
    expect(html).toMatch(/<\/span>[^<]+<\/span>/)
  })

  it('состояние вне контракта показывается как Unknown, без исключения', async () => {
    const html = await render('Exploded')
    expect(html).toContain('data-state="Unknown"')
    expect(html).toContain('rd-unk')
  })

  it('символ состояния скрыт от скринридеров', async () => {
    expect(await render('Failed')).toContain('aria-hidden="true"')
  })
})
