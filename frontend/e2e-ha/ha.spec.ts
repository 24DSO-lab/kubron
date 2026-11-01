// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, request, test, type APIRequestContext, type APIResponse, type BrowserContext, type Page } from '@playwright/test'

// Gate HA (ADR-0016): две реплики на одной PostgreSQL. Cookie у адресов с разными портами общие, поэтому один
// браузерный контекст ходит на обе реплики, как за балансировщиком без привязки. Тесты идут по порядку; последний
// останавливает реплику A.
test.describe.configure({ mode: 'serial' })

const A = process.env.E2E_HA_A_URL ?? 'http://127.0.0.1:8081'
const B = process.env.E2E_HA_B_URL ?? 'http://127.0.0.1:8082'
const PASS = 'e2e-password-1'
const TMP = 'e2e-temp-pass-1'
const JSON_HEADERS = { 'Content-Type': 'application/json' }

interface Client { ctx: BrowserContext; pageA: Page; pageB: Page; csrf: string }

const rpcOn = (c: { request: APIRequestContext }, csrf: string, base: string, method: string, args: unknown[] = []) =>
  c.request.post(`${base}/api/rpc/${method}`, { data: args, headers: { ...JSON_HEADERS, 'X-CSRF-Token': csrf } })

async function call(c: Client, base: string, method: string, args: unknown[] = []): Promise<APIResponse> {
  return rpcOn(c.ctx, c.csrf, base, method, args)
}

async function ok<T = unknown>(c: Client, base: string, method: string, args: unknown[] = []): Promise<T> {
  const body = await (await call(c, base, method, args)).json()
  expect(body.error, `${method}@${base}: ${JSON.stringify(body.error)}`).toBeUndefined()
  return body.result as T
}

async function mk(browser: import('@playwright/test').Browser): Promise<Client> {
  const ctx = await browser.newContext()
  const pageA = await ctx.newPage()
  await pageA.goto(A)
  const pageB = await ctx.newPage()
  await pageB.goto(B)
  return { ctx, pageA, pageB, csrf: '' }
}

async function login(c: Client, base: string, user: string, pass: string): Promise<APIResponse> {
  const res = await c.ctx.request.post(`${base}/api/rpc/Login`, { data: [user, pass], headers: JSON_HEADERS })
  if (res.status() === 200) c.csrf = res.headers()['x-csrf-token']
  return res
}

/** Открывает сокет событий на странице реплики; возвращает признак закрытия. */
async function watchEvents(page: Page): Promise<void> {
  await page.evaluate(() => new Promise<void>((resolve, reject) => {
    const w = window as unknown as { __closed: boolean }
    w.__closed = false
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/ws`)
    ws.onmessage = e => { if (JSON.parse(e.data as string).op === 'ready') resolve() }
    ws.onclose = () => { w.__closed = true }
    ws.onerror = () => reject(new Error('the events socket did not open'))
  }))
}
const closed = (page: Page) => page.evaluate(() => (window as unknown as { __closed: boolean }).__closed)

let admin: Client

test.beforeAll(async ({ browser }) => {
  admin = await mk(browser)
})
test.afterAll(async () => { await admin?.ctx.close() })

test('вход на реплике A, запрос на реплике B', async () => {
  const setup = await admin.ctx.request.post(`${A}/api/rpc/SetupAdmin`, { data: ['ha-admin', PASS], headers: JSON_HEADERS })
  if (setup.status() === 200) admin.csrf = setup.headers()['x-csrf-token']
  else expect((await login(admin, A, 'ha-admin', PASS)).status(), 'повторный запуск: администратор уже есть').toBe(200)

  // Cookie выдала A; B берёт сессию из PostgreSQL и строит вид сама
  const onB = await ok<{ user: { username: string } }>(admin, B, 'AuthStatus')
  expect(onB.user.username).toBe('ha-admin')
  const users = await ok<{ username: string }[]>(admin, B, 'ListUsers')
  expect(users.map(u => u.username)).toContain('ha-admin')
})

test('любая реплика обслуживает любой запрос', async () => {
  for (let i = 0; i < 10; i++) {
    const base = i % 2 === 0 ? A : B
    const st = await ok<{ user: { username: string } }>(admin, base, 'AuthStatus')
    expect(st.user.username, `${base}, запрос ${i}`).toBe('ha-admin')
  }
  // Пользователь, созданный на одной реплике, виден на другой
  await ok(admin, A, 'CreateUser', ['ha-bob', TMP, 'user'])
  // Пользователей реплика перечитывает из PostgreSQL раз в 2 с (отдельного сообщения на создание пользователя нет),
  // поэтому появление на B ждём до нескольких секунд; сессии и права распространяются сообщениями быстрее
  await expect.poll(async () => (await ok<{ username: string }[]>(admin, B, 'ListUsers')).map(u => u.username),
    { timeout: 8_000, intervals: [250] }).toContain('ha-bob')
})

test('завершение сессии на одной реплике закрывает сокет на другой, чужие сессии не затрагиваются', async ({ browser }) => {
  const victim = await mk(browser)
  const bystander = await mk(browser)
  try {
    expect((await login(victim, A, 'ha-admin', PASS)).status()).toBe(200)
    expect((await login(bystander, A, 'ha-admin', PASS)).status()).toBe(200)
    await watchEvents(victim.pageB) // сокет на реплике B, сессия выдана репликой A
    await watchEvents(bystander.pageB)

    await call(victim, A, 'Logout')
    // Сообщение session.ended доходит за секунды, не дожидаясь периодической сверки (10 с)
    await expect.poll(() => closed(victim.pageB), { timeout: 8_000, intervals: [250] }).toBe(true)
    await bystander.pageB.waitForTimeout(2_000)
    expect(await closed(bystander.pageB), 'сокет другой сессии не должен закрываться').toBe(false)
    expect((await call(bystander, B, 'AuthStatus')).status()).toBe(200)
  } finally {
    await victim.ctx.close()
    await bystander.ctx.close()
  }
})

test('блокировка входа общая: обход через разные реплики не работает', async ({ browser }) => {
  const c = await mk(browser)
  try {
    // 5 свободных попыток: после пятой логин блокируется. Попытки делятся между репликами.
    for (let i = 0; i < 5; i++) {
      const res = await login(c, i % 2 === 0 ? A : B, 'ha-bob', 'wrong-password')
      expect(res.status(), `попытка ${i + 1}`).not.toBe(200)
    }
    // Верный пароль на любой реплике отклоняется блокировкой
    for (const base of [A, B]) {
      const res = await login(c, base, 'ha-bob', TMP)
      expect(res.status(), `${base}: вход под блокировкой`).not.toBe(200)
      expect(JSON.stringify(await res.json())).toContain('too many failed login attempts')
    }
  } finally {
    await c.ctx.close()
  }
})

test('50 сессий через обе реплики', async () => {
  const N = 50
  const contexts: { ctx: APIRequestContext; csrf: string }[] = []
  const latencies: number[] = []
  try {
    for (let i = 0; i < N; i++) {
      const ctx = await request.newContext()
      const base = i % 2 === 0 ? A : B
      let res = await ctx.post(`${base}/api/rpc/Login`, { data: ['ha-admin', PASS], headers: JSON_HEADERS })
      // Лимит анонимных запросов на адрес (5/с на реплику): при отказе ждём и повторяем
      for (let t = 0; res.status() === 429 && t < 10; t++) {
        await new Promise(r => setTimeout(r, 1_000))
        res = await ctx.post(`${base}/api/rpc/Login`, { data: ['ha-admin', PASS], headers: JSON_HEADERS })
      }
      expect(res.status(), `вход ${i}`).toBe(200)
      contexts.push({ ctx, csrf: res.headers()['x-csrf-token'] })
      await new Promise(r => setTimeout(r, 250))
    }
    // Каждая сессия работает на обеих репликах
    for (let round = 0; round < 3; round++) {
      await Promise.all(contexts.map(async (c, i) => {
        const base = (i + round) % 2 === 0 ? A : B
        const t0 = Date.now()
        const res = await rpcOn({ request: c.ctx }, c.csrf, base, 'AuthStatus')
        latencies.push(Date.now() - t0)
        expect(res.status(), `сессия ${i}, ${base}`).toBe(200)
      }))
    }
    latencies.sort((x, y) => x - y)
    const p95 = latencies[Math.floor(latencies.length * 0.95)]
    console.log(`AuthStatus x${latencies.length}: p50=${latencies[Math.floor(latencies.length / 2)]}ms p95=${p95}ms max=${latencies[latencies.length - 1]}ms`)
    expect(p95, 'p95 AuthStatus, мс').toBeLessThan(2_000)
  } finally {
    await Promise.all(contexts.map(c => c.ctx.dispose()))
  }
})

test('гибель реплики: сессия действует, терминал открывается заново', async () => {
  const kubeconfig = process.env.E2E_KUBECONFIG
  const target = process.env.E2E_TERMINAL_POD // namespace/pod/container, в образе есть /bin/sh
  const pid = Number(process.env.E2E_HA_A_PID)
  test.skip(!pid, 'нужен E2E_HA_A_PID: процесс реплики A')
  const withTerminal = !!kubeconfig && !!target

  let oldTerminal = ''
  let params: [string, string, string] = ['', '', '']
  if (withTerminal) {
    const { readFileSync } = await import('node:fs')
    const meta = await ok<{ id: string }>(admin, A, 'AddClusterFromText', [readFileSync(kubeconfig!, 'utf8'), {
      displayName: 'ha-kind', icon: '', color: '', environment: '', defaultNamespace: '', contextName: '',
    }])
    await ok(admin, A, 'ConnectToCluster', [meta.id])
    params = target!.split('/') as [string, string, string]
    oldTerminal = await ok<string>(admin, A, 'OpenTerminal', [...params, '/bin/sh', 80, 24])
    expect(oldTerminal).toMatch(/^[\w-]+\.[0-9a-f]{32}$/)
  }

  process.kill(pid, 'SIGKILL') // реплика A исчезает без корректного завершения
  await expect.poll(async () => (await request.newContext().then(c => c.get(`${A}/healthz`, { timeout: 2_000 }).catch(() => null)))?.status() ?? 0,
    { timeout: 15_000 }).toBe(0)

  // Cookie, выданная A, остаётся действительной на B
  const st = await ok<{ user: { username: string } }>(admin, B, 'AuthStatus')
  expect(st.user.username).toBe('ha-admin')

  if (withTerminal) {
    // Терминал A погиб вместе с репликой; B о нём не знает и отвечает 409, клиент открывает новый
    const stale = await admin.ctx.request.get(`${B}/api/terminal/${oldTerminal}`)
    expect(stale.status()).toBe(409)
    expect(JSON.stringify(await stale.json())).toContain('stream_elsewhere')

    await ok(admin, B, 'ConnectToCluster', [(await ok<{ id: string; displayName: string }[]>(admin, B, 'GetClusterStatuses')).find(c => c.displayName === 'ha-kind')!.id])
    const fresh = await ok<string>(admin, B, 'OpenTerminal', [...params, '/bin/sh', 80, 24])
    expect(fresh).toMatch(/^[\w-]+\.[0-9a-f]{32}$/)
    expect(fresh.split('.')[0]).not.toBe(oldTerminal.split('.')[0])

    const output = await admin.pageB.evaluate(id => new Promise<string>((resolve, reject) => {
      const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/terminal/${id}`)
      ws.binaryType = 'arraybuffer'
      let out = ''
      const dec = new TextDecoder()
      ws.onopen = () => ws.send(new TextEncoder().encode('echo $((6*7))-ha\n'))
      ws.onmessage = e => {
        out += dec.decode(new Uint8Array(e.data as ArrayBuffer))
        if (/42-ha/.test(out.replace('echo $((6*7))-ha', ''))) { ws.close(); resolve(out) }
      }
      ws.onerror = () => reject(new Error('terminal socket failed'))
      setTimeout(() => reject(new Error(`no output: ${out}`)), 20_000)
    }), fresh)
    expect(output).toContain('42-ha')
    await ok(admin, B, 'CloseTerminal', [fresh])
  }
})
