// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../resources/wails', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../resources/wails')>()),
  callApp: vi.fn(),
}))

import { i18n } from '../../i18n'
import type { FluxHelmReleaseInfo, FluxKustomizationInfo } from '../../types/flux'
import FluxDeleteDialog from './FluxDeleteDialog.vue'

const ks = (p: Partial<FluxKustomizationInfo> = {}) =>
  ({ kind: 'Kustomization', name: 'apps', namespace: 'flux-system', prune: true, inventoryCount: 7, ...p }) as FluxKustomizationInfo
const hr = () => ({ kind: 'HelmRelease', name: 'web', namespace: 'apps' }) as FluxHelmReleaseInfo

const render = (object: FluxKustomizationInfo | FluxHelmReleaseInfo) =>
  renderToString(createSSRApp({ render: () => h(FluxDeleteDialog, { object }) }).use(i18n))

// Поведение по вводу (кнопка, Enter) держится на deleteConfirmed и проверяется в fluxActions.test.ts:
// тесты здесь работают без DOM и проверяют начальное состояние и разметку
describe('FluxDeleteDialog', () => {
  it('начальное состояние: кнопка удаления неактивна, поле ввода пустое и доступно', async () => {
    const html = await render(ks())
    const confirm = html.match(/<button[^>]*data-testid="flux-delete-confirm"[^>]*>/)![0]
    expect(confirm).toContain('disabled')
    const input = html.match(/<input[^>]*data-testid="flux-delete-input"[^>]*>/)![0]
    expect(input).not.toContain('disabled')
    expect(input).toContain('autocomplete="off"')
  })

  it('отмена доступна', async () => {
    const html = await render(ks())
    expect(html.match(/<button[^>]*data-testid="flux-delete-cancel"[^>]*>/)![0]).not.toContain('disabled')
  })

  it('роль и подписи: alertdialog, заголовок и описание связаны, у поля есть label', async () => {
    const html = await render(ks())
    expect(html).toContain('role="alertdialog"')
    expect(html).toContain('aria-labelledby="fd-title"')
    expect(html).toContain('aria-describedby="fd-risk"')
    expect(html).toContain('for="fd-input"')
  })

  it('Kustomization с prune=true: последствие prune с числом из inventory', async () => {
    const html = await render(ks({ prune: true, inventoryCount: 7 }))
    expect(html).toContain('data-consequence="prune"')
    expect(html).toContain('7')
  })

  it('Kustomization с prune=false: ресурсы останутся без управления', async () => {
    expect(await render(ks({ prune: false }))).toContain('data-consequence="keep"')
  })

  it('HelmRelease: uninstall', async () => {
    expect(await render(hr())).toContain('data-consequence="helmUninstall"')
  })

  it('предупреждение про Git показывается всегда', async () => {
    for (const o of [ks(), ks({ prune: false }), hr()]) expect(await render(o)).toContain('data-testid="flux-delete-git"')
  })

  it('у всех элементов есть data-testid (на них опирается e2e)', async () => {
    const html = await render(ks())
    for (const id of ['overlay', 'dialog', 'title', 'risk', 'consequence', 'git', 'input', 'cancel', 'confirm']) {
      expect(html).toContain(`data-testid="flux-delete-${id}"`)
    }
  })

  it('в заголовке — namespace/имя объекта', async () => {
    expect(await render(ks())).toContain('flux-system/apps')
  })
})
