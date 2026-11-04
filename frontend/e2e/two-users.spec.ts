// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, test, type APIResponse, type BrowserContext, type Page } from '@playwright/test'

// Gate этапа 2: два пользователя одновременно на одном сервере. Файл идёт после smoke.spec.ts
// (по алфавиту), поэтому администратор e2e-admin и кластер e2e-kind уже созданы; без них тесты
// кластера пропускаются, а проверки пользователей создают всё сами.
test.describe.configure({ mode: 'serial' })

const ADMIN = { name: 'e2e-admin', pass: 'e2e-password-1' }
const BOB = { name: 'e2e-bob', tmp: 'e2e-temp-pass-1', pass: 'e2e-bob-pass-2' }
const JSON_HEADERS = { 'Content-Type': 'application/json' }

interface Client { ctx: BrowserContext; page: Page; csrf: string }

async function call(c: Client, method: string, args: unknown[] = []): Promise<APIResponse> {
  return c.ctx.request.post(`/api/rpc/${method}`, { data: args, headers: { ...JSON_HEADERS, 'X-CSRF-Token': c.csrf } })
}

async function result<T = unknown>(c: Client, method: string, args: unknown[] = []): Promise<T> {
  const body = await (await call(c, method, args)).json()
  expect(body.error, `${method}: ${JSON.stringify(body.error)}`).toBeUndefined()
  return body.result as T
}

async function login(c: Client, user: string, pass: string): Promise<APIResponse> {
  const res = await c.ctx.request.post('/api/rpc/Login', { data: [user, pass], headers: JSON_HEADERS })
  if (res.status() === 200) c.csrf = res.headers()['x-csrf-token']
  return res
}

let admin: Client
let bob: Client

test.beforeAll(async ({ browser }) => {
  // Отдельные контексты — отдельные cookie-хранилища, как два разных браузера
  const mk = async (): Promise<Client> => {
    const ctx = await browser.newContext()
    const page = await ctx.newPage()
    await page.goto('/')
    return { ctx, page, csrf: '' }
  }
  admin = await mk()
  bob = await mk()
})

test.afterAll(async () => {
  await admin?.ctx.close()
  await bob?.ctx.close()
})

test('два пользователя входят одновременно, каждый видит себя', async () => {
  expect((await login(admin, ADMIN.name, ADMIN.pass)).status()).toBe(200)

  // Второй пользователь создаётся администратором; первый вход требует смены пароля
  const users = await result<{ username: string }[]>(admin, 'ListUsers')
  if (!users.some(u => u.username === BOB.name)) {
    await result(admin, 'CreateUser', [BOB.name, BOB.tmp, 'user'])
  }
  const first = await login(bob, BOB.name, BOB.tmp)
  if (first.status() === 200) {
    await result(bob, 'ChangePassword', [BOB.tmp, BOB.pass])
  } else {
    expect((await login(bob, BOB.name, BOB.pass)).status(), 'повторный запуск: пароль уже сменён').toBe(200)
  }

  // Вход bob не вытеснил администратора
  const a = await result<{ user: { username: string } }>(admin, 'AuthStatus')
  const b = await result<{ user: { username: string } }>(bob, 'AuthStatus')
  expect(a.user.username).toBe(ADMIN.name)
  expect(b.user.username).toBe(BOB.name)
})

test('права по роли: один вызов — разные ответы', async () => {
  expect((await call(admin, 'ListUsers')).status()).toBe(200)
  expect((await call(bob, 'ListUsers')).status()).toBe(403) // у роли user нет управления пользователями
})

test('журнал аудита: вход каждого пользователя записан от его имени с адресом', async () => {
  const page = await result<{ events: { user: string; action: string; source?: string }[] }>(
    admin, 'GetAuditLog', [{ action: 'Login', limit: 200 }])
  const logins = page.events.filter(e => e.action === 'Login')
  expect(logins.some(e => e.user === BOB.name && !!e.source)).toBe(true)
  expect(logins.some(e => e.user === ADMIN.name && !!e.source)).toBe(true)
})

test('активный кластер у каждого свой', async () => {
  const kubeconfig = process.env.E2E_KUBECONFIG
  test.skip(!kubeconfig, 'нужен E2E_KUBECONFIG и кластер e2e-kind из smoke-теста')
  const list = await result<{ id: string; displayName: string }[]>(admin, 'GetClusterStatuses')
  const cluster = list.find(c => c.displayName === 'e2e-kind')
  expect(cluster, 'кластер добавлен smoke-тестом').toBeTruthy()

  // Общая настройка LastActiveCluster не должна автоматически подключать кластер в новой веб-сессии.
  expect(await result<string>(bob, 'GetLastActiveCluster')).toBe('')
  expect(await result<string>(bob, 'GetActiveContext')).toBe('')
  await result(admin, 'ConnectToCluster', [cluster!.id])
  expect(await result<string>(admin, 'GetActiveContext')).toBe(cluster!.id)
  // Подключение администратора не сделало кластер активным у bob
  expect(await result<string>(bob, 'GetActiveContext')).toBe('')
})

test('терминал чужой сессии недоступен', async () => {
  const target = process.env.E2E_TERMINAL_POD
  test.skip(!process.env.E2E_KUBECONFIG || !target, 'нужны E2E_KUBECONFIG и E2E_TERMINAL_POD')
  const [namespace, pod, container] = target!.split('/')

  const id = await result<string>(admin, 'OpenTerminal', [namespace, pod, container, '/bin/sh', 80, 24])
  const tryOpen = (c: Client) => c.page.evaluate(
    (id) => new Promise<string>((resolve) => {
      const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/terminal/${id}`)
      ws.onopen = () => { ws.close(1000); resolve('open') }
      ws.onerror = () => resolve('refused')
      setTimeout(() => resolve('timeout'), 10000)
    }),
    id,
  )
  expect(await tryOpen(bob)).toBe('refused') // 404: чужой терминал неотличим от несуществующего
  expect(await tryOpen(admin)).toBe('open')
  await call(admin, 'CloseTerminal', [id])
})

test('события и логи доходят только владельцу', async () => {
  const target = process.env.E2E_LOG_POD // namespace/pod/container, под пишет в stdout
  test.skip(!process.env.E2E_KUBECONFIG || !target, 'нужны E2E_KUBECONFIG и E2E_LOG_POD')
  const [namespace, pod, container] = target!.split('/')
  // Идентификатор потока несёт реплику-владельца, названную в кадре ready сокета событий (ADR-0016)
  const replica = await admin.page.evaluate(() => new Promise<string>((resolve) => {
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/ws`)
    ws.onmessage = e => {
      const m = JSON.parse(e.data as string)
      if (m.op === 'ready') { ws.close(); resolve(typeof m.replica === 'string' ? m.replica : '') }
    }
  }))
  const streamId = `${replica ? replica + '.' : ''}log_e2e_${Date.now()}`

  // Обе стороны подписываются на один и тот же поток; bob — «чужой», он событий получить не должен
  const listen = (c: Client) => c.page.evaluate(
    ({ streamId }) => new Promise<void>((resolve) => {
      const w = window as unknown as { __ev: string[] }
      w.__ev = []
      const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/ws`)
      ws.onmessage = e => {
        const m = JSON.parse(e.data as string)
        if (m.op === 'sub_ok') resolve()
        if (m.event) w.__ev.push(m.event)
      }
      ws.onopen = () => ws.send(JSON.stringify({ op: 'sub', events: [`logs:data:${streamId}`, `logs:end:${streamId}`] }))
    }),
    { streamId },
  )
  await Promise.all([listen(admin), listen(bob)])

  const logDataCount = (c: Client) => c.page.evaluate(
    () => (window as unknown as { __ev: string[] }).__ev.filter(e => e.startsWith('logs:data:')).length,
  )

  await result(admin, 'StartLogStream', [streamId, namespace, pod, container, 10])
  await expect.poll(() => logDataCount(admin), { timeout: 20000 }).toBeGreaterThan(0)
  await bob.page.waitForTimeout(3000)
  expect(await bob.page.evaluate(() => (window as unknown as { __ev: string[] }).__ev.length)).toBe(0)

  // Остановить поток чужой сессии нельзя: у bob своих потоков нет, у администратора поток продолжает работать
  await call(bob, 'StopLogStream', [streamId])
  const before = await logDataCount(admin)
  await expect.poll(() => logDataCount(admin), { timeout: 10000, intervals: [250] }).toBeGreaterThan(before)
  await call(admin, 'StopLogStream', [streamId])
})

test('выход одного пользователя не затрагивает другого', async () => {
  expect((await call(admin, 'Logout')).status()).toBe(200)
  expect((await call(admin, 'ListUsers')).status()).toBe(401)
  const b = await result<{ user: { username: string } }>(bob, 'AuthStatus')
  expect(b.user.username).toBe(BOB.name)
  expect((await call(bob, 'GetActiveContext')).status()).toBe(200)
})
