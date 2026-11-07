// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { i18n } from '../../i18n'
import ChallengeDialog from './ChallengeDialog.vue'
import type { Challenge } from '../../types/credential'

const base = { busy: false, error: '', attempt: 0, remainingSec: 125, expired: false }

async function render(challenge: Challenge, over: Record<string, unknown> = {}): Promise<string> {
  const app = createSSRApp(ChallengeDialog, { challenge, ...base, ...over })
  app.use(i18n)
  return renderToString(app)
}
const ch = (p: Partial<Challenge>): Challenge => ({ id: 'c1', kind: 'credentials', expiresAt: '2030-01-01T00:00:00Z', ...p })

describe('ChallengeDialog: рисует вызов по kind', () => {
  it('роль диалога и связь с заголовком для любого вида', async () => {
    const html = await render(ch({}))
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-modal="true"')
    expect(html).toMatch(/aria-labelledby="ch-[\w-]+-title"/)
  })

  it('credentials: логин, скрытый пароль, кнопка показать', async () => {
    const html = await render(ch({ kind: 'credentials' }))
    expect(html).toContain('data-testid="field-username"')
    expect(html).toMatch(/data-testid="field-password"/)
    expect(html).toContain('type="password"')
    expect(html).toContain('data-testid="toggle-password"')
    expect(html).toContain('data-testid="challenge-submit"')
    expect(html).toContain('autocomplete="current-password"')
  })

  it('credentials: ошибка показывается в role=alert', async () => {
    const html = await render(ch({ kind: 'credentials' }), { error: 'Wrong' })
    expect(html).toMatch(/role="alert"[^>]*data-testid="challenge-error"[^>]*>Wrong</)
  })

  it('device-code: код, ссылка, обратный отсчёт, отмена; формы нет', async () => {
    const html = await render(ch({ kind: 'device-code', userCode: 'ABCD-1234', url: 'https://idp.example/device', poll: true, intervalSec: 5 }))
    expect(html).toContain('ABCD-1234')
    expect(html).toContain('data-testid="copy-code"')
    expect(html).toContain('href="https://idp.example/device"')
    expect(html).toContain('rel="noopener noreferrer"')
    expect(html).toContain('role="timer"')
    expect(html).toContain('2:05')
    expect(html).toContain('data-testid="challenge-cancel"')
    expect(html).not.toContain('data-testid="challenge-form"')
  })

  it('device-code: небезопасная ссылка не выводится', async () => {
    const html = await render(ch({ kind: 'device-code', userCode: 'X', url: 'javascript:alert(1)' }))
    expect(html).not.toContain('javascript:')
    expect(html).not.toContain('data-testid="challenge-link"')
  })

  it('device-code: после истечения предлагается повторить', async () => {
    const html = await render(ch({ kind: 'device-code', userCode: 'X' }), { expired: true, remainingSec: 0 })
    expect(html).toContain('data-testid="challenge-expired"')
    expect(html).toContain('data-testid="challenge-restart"')
  })

  it('browser-loopback: ожидание и «открыть снова»', async () => {
    const html = await render(ch({ kind: 'browser-loopback', url: 'http://127.0.0.1:8000/auth', poll: true }))
    expect(html).toContain('data-testid="challenge-waiting"')
    expect(html).toContain('data-testid="open-again"')
    expect(html).toContain('data-testid="challenge-cancel"')
  })

  it('paste-redirect: ссылка, пояснение и поле адреса', async () => {
    const html = await render(ch({ kind: 'paste-redirect', url: 'https://idp.example/auth?x=1' }))
    expect(html).toContain('data-testid="challenge-link"')
    expect(html).toContain('data-testid="paste-hint"')
    expect(html).toContain('data-testid="field-redirectUrl"')
  })

  it('secret-fields: поля по Fields, секретные скрыты', async () => {
    const html = await render(ch({ kind: 'secret-fields', fields: [{ name: 'accessKeyId', secret: false }, { name: 'secretAccessKey', secret: true }] }))
    expect(html).toContain('data-testid="field-accessKeyId"')
    expect(html).toContain('data-testid="field-secretAccessKey"')
    expect(html).toContain('data-testid="toggle-secretAccessKey"')
    expect(html).not.toContain('data-testid="toggle-accessKeyId"')
    expect(html).toMatch(/id="[^"]+-secretAccessKey"[^>]*type="password"|type="password"[^>]*id="[^"]+-secretAccessKey"/)
  })

  it('secret-fields: имя неизвестного поля показывается как есть', async () => {
    const html = await render(ch({ kind: 'secret-fields', fields: [{ name: 'tenantHint', secret: false }] }))
    expect(html).toContain('>tenantHint<')
  })

  it('manual-token: команда с экранированием только для показа и поле токена', async () => {
    const html = await render(ch({ kind: 'manual-token', command: ['aws', 'eks', 'get-token', '--cluster-name', 'my cluster'] }))
    expect(html).toContain('data-testid="manual-command"')
    expect(html).toContain('aws eks get-token --cluster-name &#39;my cluster&#39;')
    expect(html).toContain('data-testid="copy-command"')
    expect(html).toContain('data-testid="field-token"')
    expect(html).toContain('ch-masked')
  })

  it('manual-token: опасные символы в команде экранируются, не интерпретируются как разметка', async () => {
    const html = await render(ch({ kind: 'manual-token', command: ['echo', '<img src=x onerror=alert(1)>'] }))
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;img')
  })

  it('confirm-exec: команда, аргументы, только имена переменных; три кнопки', async () => {
    const html = await render(ch({
      kind: 'confirm-exec',
      exec: { apiVersion: 'client.authentication.k8s.io/v1', command: 'kubelogin', args: ['get-token', '--server-id', 'abc'], env: { AWS_PROFILE: '', TOKEN_ENV: '' } },
    }))
    expect(html).toContain('data-testid="exec-command"')
    expect(html).toContain('kubelogin get-token --server-id abc')
    expect(html).toContain('AWS_PROFILE')
    expect(html).toContain('TOKEN_ENV')
    expect(html).toContain('data-testid="decision-once"')
    expect(html).toContain('data-testid="decision-always"')
    expect(html).toContain('data-testid="challenge-cancel"')
  })

  it('confirm-exec: значения переменных окружения не выводятся, даже если пришли', async () => {
    const html = await render(ch({
      kind: 'confirm-exec',
      exec: { apiVersion: 'v1', command: 'p', args: null, env: { K: 'LEAKED-VALUE' } },
    }))
    expect(html).not.toContain('LEAKED-VALUE')
  })

  it('вывод плагина показывается текстом, разметка экранируется', async () => {
    const html = await render(ch({ kind: 'confirm-exec', message: '<b>hi</b>', exec: { apiVersion: 'v1', command: 'p', args: [] } }))
    expect(html).not.toContain('<b>hi</b>')
    expect(html).toContain('&lt;b&gt;')
  })

  it('идёт отправка: кнопка и поля заблокированы', async () => {
    const html = await render(ch({ kind: 'credentials' }), { busy: true })
    expect(html).toContain('aria-busy="true"')
    expect(html).toMatch(/data-testid="challenge-submit"[^>]*disabled|disabled[^>]*data-testid="challenge-submit"/)
  })

  it('secret-fields: многострочное поле из явного списка — маскированная textarea, а не type=password', async () => {
    const html = await render(ch({ kind: 'secret-fields', fields: [{ name: 'serviceAccountJson', secret: true }] }))
    const area = html.match(/<textarea[^>]*data-testid="field-serviceAccountJson"[^>]*>|<textarea[^>]*>/)?.[0] ?? ''
    expect(area).toContain('data-testid="field-serviceAccountJson"')
    expect(area).toContain('ch-masked')
    expect(area).toContain('autocomplete="off"')
    expect(area).toContain('spellcheck="false"')
    expect(html).not.toMatch(/<input[^>]*data-testid="field-serviceAccountJson"/)
    expect(html).not.toContain('type="password"')
    expect(html).toContain('data-testid="toggle-serviceAccountJson"')
  })

  it('secret-fields: остальные секретные поля по-прежнему type=password', async () => {
    const html = await render(ch({ kind: 'secret-fields', fields: [{ name: 'secretAccessKey', secret: true }, { name: 'serviceAccountJson', secret: true }] }))
    expect(html).toMatch(/<input[^>]*type="password"[^>]*data-testid="field-secretAccessKey"|<input[^>]*data-testid="field-secretAccessKey"[^>]*type="password"/)
    expect(html).toMatch(/<textarea[^>]*data-testid="field-serviceAccountJson"/)
  })

  it('secret-fields: подпись поля serviceAccountJson берётся из i18n, а не из имени', async () => {
    const html = await render(ch({ kind: 'secret-fields', fields: [{ name: 'serviceAccountJson', secret: true }] }))
    expect(html).toContain('>Service account JSON<')
    expect(html).not.toContain('>serviceAccountJson<')
  })

  it('secret-fields: значение не попадает в разметку вызова', async () => {
    const html = await render(ch({ kind: 'secret-fields', fields: [{ name: 'serviceAccountJson', secret: true }], message: '' }))
    expect(html).not.toContain('private_key')
  })
})
