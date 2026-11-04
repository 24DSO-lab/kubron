// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, test, type Page, type Route } from '@playwright/test'

// Сценарии диалога входа (ADR-0018, A.5) на мок-RPC: бэкенд нового контракта не нужен. Администратор
// e2e-admin создаёт smoke.spec.ts, а Playwright берёт файлы по алфавиту, поэтому имя файла начинается с
// «zz-»: он должен идти ПОСЛЕ smoke.spec.ts (как two-users и web-csp). Не переименовывайте его на имя,
// которое стоит раньше «smoke». Проверка по data-testid и ролям, не по тексту. ConnectCluster, AnswerChallenge, PollChallenge, CancelChallenge, ListMyConnections и
// DisconnectCluster подменяются через page.route; остальные вызовы идут на настоящий сервер.
test.describe.configure({ mode: 'serial' })

const USER = 'e2e-admin'
const PASSWORD = 'e2e-password-1'
const CLUSTER = {
  id: 'mock-1', displayName: 'mock-cluster', icon: '', color: '', kubeconfigFile: '', defaultNamespace: '',
  addedAt: '2026-11-01T00:00:00Z', contextName: 'ctx', environment: '', connected: false, active: false, error: '',
}
const future = () => new Date(Date.now() + 10 * 60_000).toISOString()

type Reply = unknown | ((args: unknown[]) => unknown)

interface Mock { calls: Record<string, unknown[][]> }

/** Подмена методов RPC: значение — готовый результат или функция от аргументов; ошибка — { error: message }. */
async function mockRpc(page: Page, replies: Record<string, Reply>): Promise<Mock> {
  const mock: Mock = { calls: {} }
  await page.route('**/api/rpc/*', async (route: Route) => {
    const method = new URL(route.request().url()).pathname.split('/').pop() ?? ''
    if (!(method in replies)) return route.fallback()
    const args = (route.request().postDataJSON() as unknown[]) ?? []
    ;(mock.calls[method] ??= []).push(args)
    const r = replies[method]
    const out = typeof r === 'function' ? (r as (a: unknown[]) => unknown)(args) : r
    if (out && typeof out === 'object' && 'error' in (out as object)) {
      return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: { code: 'error', message: (out as { error: string }).error } }) })
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ result: out ?? null }) })
  })
  return mock
}

async function openClusterWithMocks(page: Page, replies: Record<string, Reply>): Promise<Mock> {
  const mock = await mockRpc(page, { GetClusterStatuses: [CLUSTER], ...replies })
  await page.goto('/')
  // Нет экрана входа — администратор не создан: smoke.spec.ts не отработал (см. комментарий в начале файла)
  await expect(page.getByRole('heading', { name: 'Вход', exact: true })).toBeVisible()
  await page.locator('input[autocomplete=username]').fill(USER)
  await page.locator('input[autocomplete=current-password]').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await expect(page.getByRole('heading', { name: 'Вход', exact: true })).toHaveCount(0)
  await page.locator('.cluster-icon-wrap').first().click()
  return mock
}

const dialog = (page: Page) => page.getByTestId('challenge-dialog')

test('credentials: неверный пароль, повтор, затем вход', async ({ page }) => {
  let attempt = 0
  const mock = await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c1', kind: 'credentials', expiresAt: future() } },
    AnswerChallenge: () => (++attempt === 1 ? { error: 'the credentials were rejected' } : { status: 'connected' }),
  })
  await expect(dialog(page)).toHaveAttribute('data-kind', 'credentials')
  await page.getByTestId('field-username').fill('vasya')
  await page.getByTestId('field-password').fill('wrong')
  await page.getByTestId('field-password').press('Enter') // Enter отправляет форму
  await expect(page.getByTestId('challenge-error')).toBeVisible()
  await expect(page.getByTestId('field-password')).toHaveValue('') // пароль очищен для повтора
  await page.getByTestId('field-password').fill('right')
  await page.getByTestId('challenge-submit').click()
  await expect(dialog(page)).toHaveCount(0)
  expect(mock.calls.AnswerChallenge).toHaveLength(2)
})

test('credentials: показать и скрыть пароль, отмена по Esc', async ({ page }) => {
  const mock = await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c2', kind: 'credentials', expiresAt: future() } },
    CancelChallenge: null,
  })
  await expect(page.getByTestId('field-password')).toHaveAttribute('type', 'password')
  await page.getByTestId('toggle-password').click()
  await expect(page.getByTestId('field-password')).toHaveAttribute('type', 'text')
  await page.keyboard.press('Escape')
  await expect(dialog(page)).toHaveCount(0)
  // Диалог закрывается сразу, а CancelChallenge уходит следом: ждём, пока обработчик page.route его запишет
  await expect.poll(() => mock.calls.CancelChallenge?.[0]).toEqual(['c2'])
})

test('device-code: код, опрос и закрытие по завершению', async ({ page }) => {
  let polls = 0
  await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c3', kind: 'device-code', userCode: 'ABCD-1234', url: 'https://idp.example/device', poll: true, intervalSec: 1, expiresAt: future() } },
    PollChallenge: () => (++polls < 2 ? { status: 'challenge', challenge: { id: 'c3', kind: 'device-code', userCode: 'ABCD-1234', poll: true, intervalSec: 1, expiresAt: future() } } : { status: 'connected' }),
  })
  await expect(page.getByTestId('device-code')).toHaveText('ABCD-1234')
  await expect(page.getByTestId('challenge-countdown')).toBeVisible()
  await expect(dialog(page)).toHaveCount(0, { timeout: 15_000 })
})

test('browser-loopback: ожидание и «открыть снова»', async ({ page }) => {
  await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c4', kind: 'browser-loopback', url: 'http://127.0.0.1:8000/auth', poll: true, intervalSec: 30, expiresAt: future() } },
    CancelChallenge: null,
  })
  await expect(page.getByTestId('challenge-waiting')).toBeVisible()
  await expect(page.getByTestId('open-again')).toBeVisible()
  await page.getByTestId('challenge-cancel').click()
  await expect(dialog(page)).toHaveCount(0)
})

test('paste-redirect: пояснение и отправка адреса', async ({ page }) => {
  const mock = await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c5', kind: 'paste-redirect', url: 'https://idp.example/auth', expiresAt: future() } },
    AnswerChallenge: { status: 'connected' },
  })
  await expect(page.getByTestId('paste-hint')).toBeVisible()
  await page.getByTestId('field-redirectUrl').fill('http://localhost:8000/?code=abc&state=xyz')
  await page.getByTestId('challenge-submit').click()
  await expect(dialog(page)).toHaveCount(0)
  expect(mock.calls.AnswerChallenge?.[0]).toEqual(['c5', { redirectUrl: 'http://localhost:8000/?code=abc&state=xyz' }])
})

test('secret-fields: динамические поля', async ({ page }) => {
  await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c6', kind: 'secret-fields', fields: [{ name: 'accessKeyId', secret: false }, { name: 'secretAccessKey', secret: true }], expiresAt: future() } },
    AnswerChallenge: { status: 'connected' },
  })
  await expect(page.getByTestId('field-secretAccessKey')).toHaveAttribute('type', 'password')
  await expect(page.getByTestId('challenge-submit')).toBeDisabled()
  await page.getByTestId('field-accessKeyId').fill('AKIA')
  await page.getByTestId('field-secretAccessKey').fill('x')
  await page.getByTestId('challenge-submit').click()
  await expect(dialog(page)).toHaveCount(0)
})

test('manual-token: команда для показа и ввод токена; команда не выполняется', async ({ page }) => {
  const mock = await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c7', kind: 'manual-token', command: ['aws', 'eks', 'get-token', '--cluster-name', 'my cluster'], expiresAt: future() } },
    AnswerChallenge: { status: 'connected' },
  })
  await expect(page.getByTestId('manual-command')).toHaveText("aws eks get-token --cluster-name 'my cluster'")
  await page.getByTestId('field-token').fill('tok')
  await page.getByTestId('challenge-submit').click()
  await expect(dialog(page)).toHaveCount(0)
  expect(Object.keys(mock.calls)).not.toContain('RunCommand')
})

test('confirm-exec: «Один раз» отправляет decision=once', async ({ page }) => {
  const mock = await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c8', kind: 'confirm-exec', exec: { apiVersion: 'client.authentication.k8s.io/v1', command: 'kubelogin', args: ['get-token'], env: { AWS_PROFILE: '' } }, expiresAt: future() } },
    AnswerChallenge: { status: 'connected' },
  })
  await expect(page.getByTestId('exec-command')).toHaveText('kubelogin get-token')
  await expect(page.getByTestId('exec-env')).toContainText('AWS_PROFILE')
  await page.getByTestId('decision-once').click()
  await expect(dialog(page)).toHaveCount(0)
  expect(mock.calls.AnswerChallenge?.[0]).toEqual(['c8', { decision: 'once' }])
})

test('Tab не выходит из диалога', async ({ page }) => {
  await openClusterWithMocks(page, {
    ConnectCluster: { status: 'challenge', challenge: { id: 'c9', kind: 'credentials', expiresAt: future() } },
  })
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press('Tab')
    expect(await page.evaluate(() => !!document.activeElement?.closest('[data-testid=challenge-dialog]'))).toBe(true)
  }
})
