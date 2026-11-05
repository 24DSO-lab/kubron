// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type BrowserContext, type Page } from '@playwright/test'

// Gate этапа 3: организация → команда → кластер на PostgreSQL. Тесты идут по порядку и меняют состав команд.
//
//   pg-admin — администратор организации (первый пользователь), в командах alpha и beta НЕ состоит;
//   alice    — администратор команды alpha;  bob — администратор команды beta.
//
// Без E2E_KUBECONFIG кластеры указывают на недоступный адрес: проверки доступа от этого не зависят
// (права проверяются до обращения к кластеру), а тесты с настоящим подключением пропускаются.
test.describe.configure({ mode: 'serial' })

const PASS = 'e2e-password-1'
const TMP = 'e2e-temp-pass-1'
const JSON_HEADERS = { 'Content-Type': 'application/json' }
/** Токен первичной настройки сервисного режима (KUBRON_SETUP_TOKEN сервера, см. ci.yml) */
const SETUP_TOKEN = process.env.E2E_SETUP_TOKEN ?? ''
const KUBECONFIG_FILE = process.env.E2E_KUBECONFIG
const HAS_KIND = !!KUBECONFIG_FILE

const FAKE_KUBECONFIG = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:1", insecure-skip-tls-verify: true}
users:
- name: u
  user: {token: e2e}
contexts:
- name: ctx
  context: {cluster: c, user: u}
current-context: ctx
`
const kubeconfigText = () => (KUBECONFIG_FILE ? readFileSync(KUBECONFIG_FILE, 'utf8') : FAKE_KUBECONFIG)

interface Client { ctx: BrowserContext; page: Page; csrf: string; name: string }
interface RpcError { code: string; message: string }
interface Reply<T> { status: number; result?: T; error?: RpcError }

async function rpc<T = unknown>(c: Client, method: string, args: unknown[] = []): Promise<Reply<T>> {
  const res: APIResponse = await c.ctx.request.post(`/api/rpc/${method}`, {
    data: args, headers: { ...JSON_HEADERS, 'X-CSRF-Token': c.csrf },
  })
  const body = await res.json().catch(() => ({}))
  return { status: res.status(), result: body.result, error: body.error }
}

async function ok<T = unknown>(c: Client, method: string, args: unknown[] = []): Promise<T> {
  const r = await rpc<T>(c, method, args)
  expect(r.error, `${c.name}: ${method} ${JSON.stringify(r.error)}`).toBeUndefined()
  return r.result as T
}

/** Ответ с идентификаторами, заменёнными на ID: чужой и несуществующий кластер должны отвечать одинаково. */
function shape(r: Reply<unknown>, ids: string[]): string {
  let msg = r.error?.message ?? ''
  for (const id of ids) msg = msg.split(id).join('ID')
  return `${r.status}/${r.error?.code ?? ''}/${msg}`
}

async function signIn(c: Client, user: string, passwords: string[]): Promise<boolean> {
  for (const p of passwords) {
    const res = await c.ctx.request.post('/api/rpc/Login', { data: [user, p], headers: JSON_HEADERS })
    if (res.status() === 200) {
      c.csrf = res.headers()['x-csrf-token']
      return true
    }
  }
  return false
}

let admin: Client, alice: Client, bob: Client
let alphaId = '', betaId = '', aliceId = '', bobId = '', alphaCluster = '', betaCluster = ''
const MISSING = 'no-such-cluster'

test.beforeAll(async ({ browser }) => {
  const mk = async (name: string): Promise<Client> => {
    const ctx = await browser.newContext()
    const page = await ctx.newPage()
    await page.goto('/')
    return { ctx, page, csrf: '', name }
  }
  admin = await mk('pg-admin')
  alice = await mk('alice')
  bob = await mk('bob')
})

test.afterAll(async () => {
  for (const c of [admin, alice, bob]) await c?.ctx.close()
})

test('первичная настройка по сети без токена отклоняется', async () => {
  const st = await admin.ctx.request.post('/api/rpc/AuthStatus', { data: [], headers: JSON_HEADERS })
  const status = (await st.json()).result
  test.skip(!status.needsSetup, 'повторный запуск: администратор уже создан')
  expect(status.setupTokenRequired).toBe(true)
  for (const headers of [JSON_HEADERS, { ...JSON_HEADERS, 'X-Kubron-Setup-Token': 'wrong-token-0123456789' }]) {
    const res = await admin.ctx.request.post('/api/rpc/SetupAdmin', { data: ['intruder', PASS], headers })
    expect(res.status()).toBe(403)
    expect((await res.json()).error.code).toBe('setup_token_invalid')
  }
})

test('подготовка: администратор организации, две команды, два администратора команд', async () => {
  const setup = await admin.ctx.request.post('/api/rpc/SetupAdmin', { data: ['pg-admin', PASS], headers: { ...JSON_HEADERS, 'X-Kubron-Setup-Token': SETUP_TOKEN } })
  if (setup.status() === 200) admin.csrf = setup.headers()['x-csrf-token']
  else expect(await signIn(admin, 'pg-admin', [PASS]), 'первый запуск или вход под созданным администратором').toBe(true)

  const alpha = await ok<{ id: string }>(admin, 'CreateTeam', ['alpha'])
  const beta = await ok<{ id: string }>(admin, 'CreateTeam', ['beta'])
  alphaId = alpha.id
  betaId = beta.id

  for (const u of ['alice', 'bob']) await ok(admin, 'CreateUser', [u, TMP, 'user'])
  for (const c of [alice, bob]) {
    expect(await signIn(c, c.name, [TMP]), `первый вход ${c.name}`).toBe(true)
    await ok(c, 'ChangePassword', [TMP, PASS])
  }
  const users = await ok<{ id: string; username: string }[]>(admin, 'ListUsers')
  aliceId = users.find(u => u.username === 'alice')!.id
  bobId = users.find(u => u.username === 'bob')!.id

  await ok(admin, 'SetTeamMember', [alphaId, aliceId, 'admin'])
  await ok(admin, 'SetTeamMember', [betaId, bobId, 'admin'])

  const settings = (displayName: string) => ({
    displayName, icon: '', color: '', environment: '', defaultNamespace: '', contextName: '',
  })
  alphaCluster = (await ok<{ id: string }>(alice, 'AddClusterToTeam', [alphaId, kubeconfigText(), settings('alpha-cluster')])).id
  betaCluster = (await ok<{ id: string }>(bob, 'AddClusterToTeam', [betaId, kubeconfigText(), settings('beta-cluster')])).id
  expect(alphaCluster).not.toBe(betaCluster)
})

const names = async (c: Client) =>
  (await ok<{ displayName: string }[]>(c, 'GetClusterStatuses')).map(s => s.displayName).filter(n => n.endsWith('-cluster')).sort()

test('каждый видит только кластеры своей команды', async () => {
  expect(await names(alice)).toEqual(['alpha-cluster'])
  expect(await names(bob)).toEqual(['beta-cluster'])
})

test('чужой кластер неотличим от несуществующего', async () => {
  const probes: [string, (id: string) => unknown[]][] = [
    ['ConnectCluster', id => [id]],
    ['GetClusterKubeconfig', id => [id]],
    ['ResetClusterSharedAccount', id => [id]],
    ['MoveCluster', id => [id, betaId]],
    ['DeleteCluster', id => [id]],
  ]
  for (const [method, args] of probes) {
    const foreign = await rpc(bob, method, args(alphaCluster))
    const missing = await rpc(bob, method, args(MISSING))
    expect(foreign.error, `${method}: чужой кластер не должен быть доступен`).toBeDefined()
    expect(shape(foreign, [alphaCluster]), method).toBe(shape(missing, [MISSING]))
  }
  // Кластер alpha цел: попытки bob его не тронули
  expect(await names(alice)).toEqual(['alpha-cluster'])
})

test('администратор организации без членства не читает данные кластера', async () => {
  expect(await names(admin)).toEqual([])
  const foreign = await rpc(admin, 'ConnectCluster', [alphaCluster])
  const missing = await rpc(admin, 'ConnectCluster', [MISSING])
  expect(foreign.error).toBeDefined()
  expect(shape(foreign, [alphaCluster])).toBe(shape(missing, [MISSING]))
  expect(shape(await rpc(admin, 'GetClusterKubeconfig', [alphaCluster]), [alphaCluster]))
    .toBe(shape(await rpc(admin, 'GetClusterKubeconfig', [MISSING]), [MISSING]))
  // Нет активного кластера — нет и данных
  expect((await rpc(admin, 'GetNamespaces')).error).toBeDefined()
  // А управлять командами он может: видит их и состав
  const teams = await ok<{ id: string }[]>(admin, 'ListTeams')
  expect(teams.map(t => t.id)).toEqual(expect.arrayContaining([alphaId, betaId]))
  expect((await rpc(admin, 'ListTeamMembers', [alphaId])).error).toBeUndefined()
})

test('администратор команды управляет своей командой и не трогает чужую', async () => {
  expect((await rpc(alice, 'ListTeamMembers', [alphaId])).error).toBeUndefined()
  expect((await rpc(alice, 'ListTeamMembers', [betaId])).error).toBeDefined()
  expect((await rpc(alice, 'SetTeamMember', [betaId, aliceId, 'admin'])).error).toBeDefined()
  expect((await rpc(alice, 'AddClusterToTeam', [betaId, kubeconfigText(), {
    displayName: 'sneaky-cluster', icon: '', color: '', environment: '', defaultNamespace: '', contextName: '',
  }])).error).toBeDefined()
  expect((await rpc(alice, 'CreateTeam', ['gamma'])).error, 'создавать команды может только администратор организации').toBeDefined()

  // alice добавляет bob в alpha обычным участником
  await ok(alice, 'SetTeamMember', [alphaId, bobId, 'user'])
  expect(await names(bob)).toEqual(['alpha-cluster', 'beta-cluster'])
  // Обычный участник не управляет ни составом, ни кластерами команды
  expect((await rpc(bob, 'SetTeamMember', [alphaId, bobId, 'admin'])).error).toBeDefined()
  expect((await rpc(bob, 'AddClusterToTeam', [alphaId, kubeconfigText(), {
    displayName: 'bob-cluster', icon: '', color: '', environment: '', defaultNamespace: '', contextName: '',
  }])).error).toBeDefined()
  expect((await rpc(bob, 'DeleteCluster', [alphaCluster])).error).toBeDefined()
})

test('исключённый участник теряет кластер сразу', async () => {
  await ok(alice, 'RemoveTeamMember', [alphaId, bobId])
  expect(await names(bob)).toEqual(['beta-cluster'])
  const r = await rpc(bob, 'ConnectCluster', [alphaCluster])
  expect(shape(r, [alphaCluster])).toBe(shape(await rpc(bob, 'ConnectCluster', [MISSING]), [MISSING]))
})

test('потеря доступа закрывает уже открытое подключение', async () => {
  test.skip(!HAS_KIND, 'нужен E2E_KUBECONFIG: подключение к настоящему кластеру')
  await ok(alice, 'SetTeamMember', [alphaId, bobId, 'user'])
  await ok(bob, 'ConnectCluster', [alphaCluster])
  expect(await ok<string>(bob, 'GetActiveContext')).toBe(alphaCluster)
  expect((await ok<{ name: string }[]>(bob, 'GetNamespaces')).map(n => n.name)).toContain('kube-system')

  // Подписка на событие: клиент должен узнать, что доступ закрыт
  await bob.page.evaluate(() => new Promise<void>((resolve) => {
    const w = window as unknown as { __revoked: string[] }
    w.__revoked = []
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/ws`)
    ws.onmessage = e => {
      const m = JSON.parse(e.data as string)
      if (m.op === 'sub_ok') resolve()
      if (m.event === 'cluster:access-revoked') w.__revoked.push(String(m.data?.[0] ?? ''))
    }
    ws.onopen = () => ws.send(JSON.stringify({ op: 'sub', events: ['cluster:access-revoked'] }))
  }))

  await ok(alice, 'RemoveTeamMember', [alphaId, bobId])
  // Сервер проверяет права открытых сессий раз в 10 с
  await expect.poll(() => ok<string>(bob, 'GetActiveContext'), { timeout: 40_000, intervals: [1000] }).toBe('')
  await expect.poll(() => bob.page.evaluate(() => (window as unknown as { __revoked: string[] }).__revoked),
    { timeout: 10_000 }).toContain(alphaCluster)
  expect((await rpc(bob, 'GetNamespaces')).error).toBeDefined()
  // Свой кластер у bob по-прежнему доступен
  await ok(bob, 'ConnectCluster', [betaCluster])
  expect(await ok<string>(bob, 'GetActiveContext')).toBe(betaCluster)
})

test('журнал аудита: администратор команды видит только события своей команды', async () => {
  type Ev = { teamId?: string; user: string; action: string }
  const mine = (await ok<{ events: Ev[] }>(alice, 'GetAuditLog', [{ limit: 500 }])).events
  expect(mine.some(e => e.teamId === alphaId), 'события alpha видны').toBe(true)
  expect(mine.some(e => e.teamId === betaId), 'события beta скрыты').toBe(false)

  const all = (await ok<{ events: Ev[] }>(admin, 'GetAuditLog', [{ limit: 500 }])).events
  expect(all.some(e => e.teamId === alphaId) && all.some(e => e.teamId === betaId), 'админ организации видит обе команды').toBe(true)

  // Обычному участнику журнал не положен
  const member = await rpc(bob, 'GetAuditLog', [{ team: alphaId, limit: 10 }])
  const events = (member.result as { events: Ev[] } | undefined)?.events ?? []
  expect(events.some(e => e.teamId === alphaId)).toBe(false)
})

test('интерфейс: в полосе кластеров только свои, раздел «Команды» открывается без кластера', async ({ browser }) => {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  try {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Вход', exact: true })).toBeVisible()
    await page.locator('input[autocomplete=username]').fill('alice')
    await page.locator('input[autocomplete=current-password]').fill(PASS)
    await page.getByRole('button', { name: 'Войти' }).click()
    await expect(page.locator('.cluster-bar')).toBeVisible()
    await expect(page.locator('.cluster-icon-wrap[aria-label="alpha-cluster"]')).toBeVisible()
    await expect(page.locator('.cluster-icon-wrap[aria-label="beta-cluster"]')).toHaveCount(0)
    // Боковое меню есть только при подключении к кластеру, поэтому разделы управления доступны и с экрана выбора кластера
    await page.locator('[data-testid="management-links"]').getByRole('button', { name: 'Команды', exact: true }).click()
    await expect(page.locator('[data-testid="management"]')).toBeVisible()
    await expect(page.locator('[data-testid="management"]').getByText('alpha').first()).toBeVisible()
  } finally {
    await ctx.close()
  }
})
