// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { i18n } from '../../i18n'
import GKEOAuthSettings from './GKEOAuthSettings.vue'

async function render(): Promise<string> {
  const app = createSSRApp(GKEOAuthSettings)
  app.use(i18n)
  return renderToString(app)
}

describe('GKEOAuthSettings', () => {
  it('секрет вводится в поле type=password, а подпись берётся из i18n', async () => {
    const html = await render()
    expect(html).toMatch(/<input[^>]*type="password"[^>]*data-testid="gke-oauth-client-secret"|<input[^>]*data-testid="gke-oauth-client-secret"[^>]*type="password"/)
    expect(html).toContain('autocomplete="off"')
    expect(html).toContain('>Client secret<')
    expect(html).toContain('Google sign-in for GKE')
    expect(html).not.toContain('credential.gkeOAuth')
  })

  it('до ответа бэкенда состояние «недоступно», а сохранить и удалить нельзя', async () => {
    const html = await render()
    expect(html).toContain('The setting is not available.')
    expect(html).toMatch(/<button[^>]*disabled[^>]*data-testid="gke-oauth-save"|<button[^>]*data-testid="gke-oauth-save"[^>]*disabled/)
    expect(html).toMatch(/<button[^>]*disabled[^>]*data-testid="gke-oauth-clear"|<button[^>]*data-testid="gke-oauth-clear"[^>]*disabled/)
  })

  it('в разметке нет значения секрета', async () => {
    const html = await render()
    expect(html).not.toMatch(/data-testid="gke-oauth-client-secret"[^>]*value=/)
  })
})
