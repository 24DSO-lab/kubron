// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { i18n } from '../../i18n'

vi.mock('../../platform/bugreport', () => ({
  getBugReportStatus: vi.fn(async () => { throw new Error('not in SSR') }),
  saveBugReportSettings: vi.fn(),
  clearBugReportSettings: vi.fn(),
  submitBugReport: vi.fn(),
}))

import BugReportDialog from './BugReportDialog.vue'

async function render(): Promise<string> {
  const app = createSSRApp(BugReportDialog)
  app.use(i18n)
  return renderToString(app)
}

describe('BugReportDialog', () => {
  it('подписи берутся из i18n, а не из ключей', async () => {
    const html = await render()
    expect(html).toContain('Report a problem')
    expect(html).toContain('What happened?')
    expect(html).not.toContain('bugReport.')
  })

  it('пока состояние почты неизвестно, действий отправки нет: только «Отмена»', async () => {
    const html = await render()
    expect(html).not.toContain('data-testid="bug-report-send"')
    expect(html).not.toContain('data-testid="bug-report-export"')
    expect(html).toContain('data-testid="bug-report-cancel"')
  })

  it('поле описания ограничено по длине', async () => {
    expect(await render()).toMatch(/<textarea[^>]*maxlength="5000"/)
  })
})
