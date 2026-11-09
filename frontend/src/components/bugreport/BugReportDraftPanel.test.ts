// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'
import { createSSRApp, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { i18n } from '../../i18n'
import type { BugReportDraft, BugReportStatus } from '../../types/bugreport'

const phase = ref<string>('draft')
const draft = ref<(BugReportDraft & { copied: boolean }) | null>(null)
const status = ref<BugReportStatus | null>(null)

vi.mock('../../composables/useBugReport', () => ({
  useBugReport: () => ({
    phase, draft, status,
    screenshot: ref('data:image/png;base64,AAAA'), captureFailed: ref(false), includeScreenshot: ref(true),
    description: ref('broken'), contact: ref(''), error: ref(''),
    retake: vi.fn(), send: vi.fn(), openInMailClient: vi.fn(), revealFile: vi.fn(), backToReport: vi.fn(),
    close: vi.fn(), refreshStatus: vi.fn(),
  }),
}))
vi.mock('../../platform/bugreport', () => ({
  getBugReportStatus: vi.fn(async () => { throw new Error('not in SSR') }),
  saveBugReportSettings: vi.fn(), clearBugReportSettings: vi.fn(), submitBugReport: vi.fn(),
  openBugReportDraft: vi.fn(), revealBugReportFile: vi.fn(),
}))

import BugReportDialog from './BugReportDialog.vue'

const settings = { host: '', port: 587, username: '', password: '', from: '', to: '', security: 'starttls' as const, passwordSet: false }
const st = (over: Partial<BugReportStatus>): BugReportStatus =>
  ({ configured: false, source: '', recipient: 'dev@example.org', canSave: true, canDraft: true, settings, ...over })

async function render(): Promise<string> {
  const app = createSSRApp(BugReportDialog)
  app.use(i18n)
  return renderToString(app)
}

describe('BugReportDialog: почтовая программа', () => {
  it('после открытия письма показывает, как вложить снимок, и путь к файлу', async () => {
    phase.value = 'draft'
    status.value = st({})
    draft.value = { screenshotPath: '/home/u/.kubron/bugreports/kubron-bug-1.png', textPath: '', truncated: false, copied: true }
    const html = await render()
    expect(html).toContain('data-testid="bug-report-draft"')
    expect(html).toContain('dev@example.org')
    expect(html).toContain('data-testid="bug-report-draft-paste"')
    expect(html).toContain('kubron-bug-1.png')
    expect(html).not.toContain('data-testid="bug-report-description"') // форма скрыта
    expect(html).not.toContain('bugReport.')
  })

  it('если снимок не скопировался, просит приложить файл; сокращённый текст — отдельное предупреждение', async () => {
    phase.value = 'draft'
    draft.value = { screenshotPath: '/d/kubron-bug-1.png', textPath: '/d/kubron-bug-1.txt', truncated: true, copied: false }
    const html = await render()
    expect(html).toContain('Attach this file')
    expect(html).toContain('data-testid="bug-report-draft-truncated"')
    expect(html).toContain('kubron-bug-1.txt')
  })

  it('без почтового сервера основная кнопка — «Открыть в почтовой программе», «Отправить» нет', async () => {
    phase.value = 'open'
    draft.value = null
    status.value = st({ configured: false })
    const html = await render()
    expect(html).toContain('data-testid="bug-report-mailclient"')
    expect(html).toContain('data-testid="bug-report-mailclient-hint"')
    expect(html).not.toContain('data-testid="bug-report-send"')
  })

  it('с настроенным сервером есть оба способа', async () => {
    phase.value = 'open'
    status.value = st({ configured: true })
    const html = await render()
    expect(html).toContain('data-testid="bug-report-mailclient"')
    expect(html).toContain('data-testid="bug-report-send"')
  })

  it('на сервере (нет черновика и нет прав на настройки) кнопки почтовой программы нет', async () => {
    phase.value = 'open'
    status.value = st({ configured: true, canDraft: false, canSave: false })
    const html = await render()
    expect(html).not.toContain('data-testid="bug-report-mailclient"')
    expect(html).not.toContain('data-testid="bug-report-mail-toggle"')
    expect(html).toContain('data-testid="bug-report-send"')
  })
})
