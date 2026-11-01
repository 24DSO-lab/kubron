// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type Browser, type BrowserContext, type Page } from '@playwright/test'

// Этап 5: port-forward через прокси потоков, терминалы и флаги отключения (docs/e2e.md).
// Нужны отдельные серверы с пустыми каталогами данных:
//   E2E_PF_URL     — сервер с KUBRON_PF_BASE_DOMAIN=localhost:<порт> KUBRON_PF_SCHEME=http
//                    KUBRON_TERMINAL_IDLE_TIMEOUT=10s (например, http://localhost:8081);
//   E2E_LOCKED_URL — сервер с KUBRON_DISABLE_EXEC=1 и KUBRON_DISABLE_PORT_FORWARD=1 (http://127.0.0.1:8082).
// Дополнительно: E2E_KUBECONFIG, E2E_HTTP_POD=namespace/pod (HTTP на порту 8080, отвечает «kubron-pf-ok»),
// E2E_TERMINAL_POD=namespace/pod/container.
test.describe.configure({ mode: 'serial' })

const ADMIN = { name: 'pf-admin', pass: 'pf-password-1' }
const BOB = { name: 'pf-bob', tmp: 'pf-temp-pass-1', pass: 'pf-bob-pass-2' }
const JSON_HEADERS = { 'Content-Type': 'application/json' }

interface Client { ctx: BrowserContext; page: Page; csrf: string }

const PF_URL = process.env.E2E_PF_URL
const LOCKED_URL = process.env.E2E_LOCKED_URL
const KUBECONFIG = process.env.E2E_KUBECONFIG
const HTTP_POD = process.env.E2E_HTTP_POD
const TERMINAL_POD = process.env.E2E_TERMINAL_POD

async function call(c: Client, method: string, args: unknown[] = []): Promise<APIResponse> {
  return c.ctx.request.post(`/api/rpc/${method}`, { data: args, headers: { ...JSON_HEADERS, 'X-CSRF-Token': c.csrf } })
}

async function result<T = unknown>(c: Client, method: string, args: unknown[] = []): Promise<T> {
  const body = await (await call(c, method, args)).json()
  expect(body.error, `${method}: ${JSON.stringify(body.error)}`).toBeUndefined()
  return body.result as T
}

/** Ответ метода целиком: ошибка не валит тест, её проверяет вызывающий. */
async function raw(c: Client, method: string, args: unknown[] = []): Promise<{ status: number; body: any }> {
  const res = await call(c, method, args)
  return { status: res.status(), body: await res.json().catch(() => ({})) }
}

async function open(browser: Browser, baseURL: string): Promise<Client> {
  const ctx = await browser.newContext({ baseURL })
  const page = await ctx.newPage()
  await page.goto('/')
  return { ctx, page, csrf: '' }
}

/** Первый вход на пустом сервере создаёт администратора; повторный запуск просто входит. */
async function adminLogin(c: Client) {
  const setup = await c.ctx.request.post('/api/rpc/SetupAdmin', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  const res = setup.status() === 200
    ? setup
    : await c.ctx.request.post('/api/rpc/Login', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  expect(res.status()).toBe(200)
  c.csrf = res.headers()['x-csrf-token']
  expect(c.csrf).toBeTruthy()
}

async function addCluster(c: Client): Promise<string> {
  const text = readFileSync(KUBECONFIG!, 'utf8')
  const meta = await result<{ id: string }>(c, 'AddClusterFromText', [text, { displayName: 'pf-kind' }])
  await result(c, 'ConnectToCluster', [meta.id])
  return meta.id
}

/** Выполняет команду в терминале по WebSocket и возвращает вывод, пока он не содержит want. */
async function runInTerminal(page: Page, id: string, command: string, want: string): Promise<string> {
  return page.evaluate(({ id, command, want }) => new Promise<string>((resolve, reject) => {
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/terminal/${id}`)
    ws.binaryType = 'arraybuffer'
    let out = ''
    const timer = setTimeout(() => reject(new Error('нет вывода команды: ' + out)), 20000)
    ws.onopen = () => ws.send(new TextEncoder().encode(command + '\n'))
    ws.onmessage = e => {
      out += new TextDecoder().decode(e.data as ArrayBuffer)
      if (out.includes(want)) { clearTimeout(timer); ws.close(1000); resolve(out) }
    }
    ws.onerror = () => reject(new Error('ошибка WebSocket'))
  }), { id, command, want })
}

async function auditText(c: Client, action: string): Promise<string> {
  const page = await result<unknown>(c, 'GetAuditLog', [{ action, limit: 200 }])
  return JSON.stringify(page)
}

test.describe('port-forward в режиме proxy', () => {
  test.skip(!PF_URL || !KUBECONFIG || !HTTP_POD, 'нужны E2E_PF_URL, E2E_KUBECONFIG и E2E_HTTP_POD')
  let admin: Client
  let bob: Client
  let streamId = ''
  let entry = ''

  test.beforeAll(async ({ browser }) => {
    admin = await open(browser, PF_URL!)
    bob = await open(browser, PF_URL!)
    await adminLogin(admin)
    await addCluster(admin)
  })
  test.afterAll(async () => { await admin?.ctx.close(); await bob?.ctx.close() })

  test('capabilities: режим proxy, терминалы и оболочка узла доступны', async () => {
    const caps = await result<Record<string, unknown>>(admin, 'GetCapabilities')
    expect(caps).toMatchObject({ portForward: true, portForwardMode: 'proxy', exec: true })
  })

  test('порт на хосте не открывается, адрес приходит в ответе и открывается в браузере', async () => {
    const [namespace, pod] = HTTP_POD!.split('/')
    const s = await result<{ id: string; mode: string; localPort: number }>(admin, 'StartPortForward', [namespace, pod, 0, 8080])
    streamId = s.id
    expect(s.mode).toBe('proxy')
    expect(s.localPort).toBe(0) // net.Listen на хосте не вызывался

    entry = await result<string>(admin, 'GetPortForwardURL', [streamId])
    expect(entry).toMatch(new RegExp(`^http://${streamId}\\.pf\\.localhost:\\d+/`))

    // Тот же браузерный контекст, но другой origin: cookie Kubron на хост потока не уходит
    const page = await admin.ctx.newPage()
    const res = await page.goto(entry)
    expect(res!.status()).toBe(200)
    await expect(page.locator('body')).toContainText('kubron-pf-ok')
    const cookies = await admin.ctx.cookies(entry)
    expect(cookies.find(c => c.name === 'kpf'), 'cookie потока').toBeTruthy()
    expect(cookies.find(c => c.name.endsWith('kubron_session')), 'cookie сессии не должна быть на хосте потока').toBeUndefined()
    // Хост потока не обслуживает API Kubron
    const api = await page.request.post(new URL('/api/rpc/ListUsers', entry).toString(), { data: [], headers: JSON_HEADERS })
    expect(api.status(), 'API Kubron недоступен на хосте потока').not.toBe(200)
    await page.close()
  })

  test('адрес одноразовый: повтор в другом контексте не даёт доступа', async ({ browser }) => {
    const stranger = await browser.newContext()
    const page = await stranger.newPage()
    const res = await page.goto(entry)
    expect([401, 403, 404]).toContain(res!.status())
    await expect(page.locator('body')).not.toContainText('kubron-pf-ok')
    // Без ticket поток тоже закрыт
    const bare = await page.goto(new URL('/', entry).toString())
    expect([401, 403, 404]).toContain(bare!.status())
    await stranger.close()
  })

  test('чужой пользователь не получает адрес и не может остановить поток', async () => {
    await result(admin, 'CreateUser', [BOB.name, BOB.tmp, 'user'])
    const first = await bob.ctx.request.post('/api/rpc/Login', { data: [BOB.name, BOB.tmp], headers: JSON_HEADERS })
    bob.csrf = first.headers()['x-csrf-token']
    await result(bob, 'ChangePassword', [BOB.tmp, BOB.pass])

    const url = await raw(bob, 'GetPortForwardURL', [streamId])
    expect(url.body.error ?? url.status, 'чужой поток неотличим от несуществующего').toBeTruthy()
    expect(JSON.stringify(url.body)).not.toContain(streamId + '.pf.')
    const stop = await raw(bob, 'StopPortForward', [streamId])
    expect(stop.body.error ?? stop.status).toBeTruthy()
    const mine = await result<{ id: string }[]>(bob, 'ListPortForwards')
    expect(mine ?? []).toHaveLength(0)
  })

  test('остановка закрывает поток; начало и конец записаны в аудит', async () => {
    await result(admin, 'StopPortForward', [streamId])
    const fresh = await result<string>(admin, 'GetPortForwardURL', [streamId]).catch(() => '')
    expect(fresh).toBe('')

    const log = await auditText(admin, 'portforward.')
    expect(log).toContain('portforward.start')
    expect(log).toContain('portforward.stop')
    expect(log).toContain(streamId)
    expect(log).not.toContain('ticket=') // секреты потока в журнал не пишутся
  })
})

test.describe('терминалы: аудит, сроки, владелец', () => {
  test.skip(!PF_URL || !KUBECONFIG || !TERMINAL_POD, 'нужны E2E_PF_URL, E2E_KUBECONFIG и E2E_TERMINAL_POD')
  let admin: Client
  const [namespace, pod, container] = (TERMINAL_POD ?? '//').split('/')

  test.beforeAll(async ({ browser }) => {
    admin = await open(browser, PF_URL!)
    await adminLogin(admin)
    const list = await result<{ displayName: string; id: string }[]>(admin, 'GetClusterStatuses')
    if (!list.some(c => c.displayName === 'pf-kind')) await addCluster(admin)
    else await result(admin, 'ConnectToCluster', [list.find(c => c.displayName === 'pf-kind')!.id])
  })
  test.afterAll(async () => { await admin?.ctx.close() })

  test('сессия терминала записывается в аудит с причиной и длительностью', async () => {
    const id = await result<string>(admin, 'OpenTerminal', [namespace, pod, container, '/bin/sh', 80, 24])
    const out = await runInTerminal(admin.page, id, 'echo kubron-$((20+22))', 'kubron-42')
    expect(out).toContain('kubron-42')
    await result(admin, 'CloseTerminal', [id])

    await expect.poll(() => auditText(admin, 'terminal.')).toContain('terminal.stop')
    const log = await auditText(admin, 'terminal.')
    expect(log).toContain('terminal.start')
    expect(log).toContain(`pod=${pod}`)
    expect(log).toMatch(/reason=closed duration=/)
  })

  test('терминал без ввода закрывается по idle-таймауту (KUBRON_TERMINAL_IDLE_TIMEOUT=10s)', async () => {
    const id = await result<string>(admin, 'OpenTerminal', [namespace, pod, container, '/bin/sh', 80, 24])
    const closed = await admin.page.evaluate((id) => new Promise<string>((resolve, reject) => {
      const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/terminal/${id}`)
      ws.binaryType = 'arraybuffer'
      let out = ''
      const timer = setTimeout(() => reject(new Error('терминал не закрылся: ' + out)), 40000)
      ws.onmessage = e => { out += new TextDecoder().decode(e.data as ArrayBuffer) }
      ws.onclose = () => { clearTimeout(timer); resolve(out) }
    }), id)
    expect(closed).toContain('closed after being idle')
    await expect.poll(() => auditText(admin, 'terminal.')).toContain('reason=idle')
  })

  test('чужой веб-сеанс не подключается к терминалу и вводу', async ({ browser }) => {
    const id = await result<string>(admin, 'OpenTerminal', [namespace, pod, container, '/bin/sh', 80, 24])
    const other = await open(browser, PF_URL!)
    const res = await other.page.evaluate(async (id) => {
      const r = await fetch(`/api/terminal/${id}`)
      return r.status
    }, id)
    expect([401, 404]).toContain(res) // чужой и несуществующий терминал неотличимы
    await other.ctx.close()
    await result(admin, 'CloseTerminal', [id])
  })
})

test.describe('флаги отключения', () => {
  test.skip(!LOCKED_URL || !KUBECONFIG || !TERMINAL_POD, 'нужны E2E_LOCKED_URL, E2E_KUBECONFIG и E2E_TERMINAL_POD')
  let admin: Client
  test.beforeAll(async ({ browser }) => {
    admin = await open(browser, LOCKED_URL!)
    await adminLogin(admin)
    await addCluster(admin)
  })
  test.afterAll(async () => { await admin?.ctx.close() })

  test('GetCapabilities отражает KUBRON_DISABLE_*', async () => {
    const caps = await result<Record<string, unknown>>(admin, 'GetCapabilities')
    expect(caps).toMatchObject({ exec: false, nodeShell: false, portForward: false })
  })

  test('методы отвечают понятной ошибкой, а не пускают', async () => {
    const [namespace, pod, container] = TERMINAL_POD!.split('/')
    const term = await raw(admin, 'OpenTerminal', [namespace, pod, container, '/bin/sh', 80, 24])
    expect(JSON.stringify(term.body.error)).toMatch(/disabled/i)
    const shell = await raw(admin, 'StartNodeShell', ['any-node'])
    expect(JSON.stringify(shell.body.error)).toMatch(/disabled/i)
    const pf = await raw(admin, 'StartPortForward', [namespace, pod, 0, 8080])
    expect(JSON.stringify(pf.body.error)).toMatch(/disabled/i)
  })
})
