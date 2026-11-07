// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type Page } from '@playwright/test'

// Один сервер с пустым каталогом данных на весь файл: тесты идут по порядку и зависят друг от друга.
test.describe.configure({ mode: 'serial' })

const USER = 'e2e-admin'
const PASSWORD = 'e2e-password-1'
const JSON_HEADERS = { 'Content-Type': 'application/json' }

async function rpc(page: Page, method: string, args: unknown[], csrf?: string): Promise<APIResponse> {
  return page.request.post(`/api/rpc/${method}`, {
    data: args,
    headers: csrf ? { ...JSON_HEADERS, 'X-CSRF-Token': csrf } : JSON_HEADERS,
  })
}

async function csrfOf(page: Page): Promise<string> {
  const res = await page.request.get('/api/session')
  const body = await res.json()
  expect(body.authenticated).toBe(true)
  return body.csrfToken
}

async function logIn(page: Page) {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Вход', exact: true })).toBeVisible()
  await page.locator('input[autocomplete=username]').fill(USER)
  await page.locator('input[autocomplete=current-password]').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await expect(page.locator('.welcome-screen, .cluster-bar, nav')).not.toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Вход', exact: true })).toHaveCount(0)
}

test('первый запуск: создание администратора и вход в интерфейс', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Добро пожаловать в Kubron', exact: true })).toBeVisible()
  await page.locator('input[autocomplete=username]').fill(USER)
  await page.locator('input[autocomplete=new-password]').nth(0).fill(PASSWORD)
  await page.locator('input[autocomplete=new-password]').nth(1).fill(PASSWORD)
  await page.getByRole('button', { name: 'Создать администратора' }).click()
  // Кластеров ещё нет: приветственный экран приложения
  await expect(page.locator('.welcome-screen')).toBeVisible()
})

test('cookie сессии: HttpOnly, SameSite=Lax; без cookie сессия не восстанавливается', async ({ page, context }) => {
  await page.goto('/')
  await rpc(page, 'Login', [USER, PASSWORD])
  const cookie = (await context.cookies()).find(c => c.name.endsWith('kubron_session'))
  expect(cookie, 'cookie сессии').toBeTruthy()
  expect(cookie!.httpOnly).toBe(true)
  expect(cookie!.sameSite).toBe('Lax')

  await context.clearCookies()
  await logIn(page) // экран входа, затем успешный вход
})

test('защита API: сессия, CSRF, чужой Origin', async ({ page, playwright, baseURL }) => {
  // Без сессии защищённые методы закрыты, публичные — нет
  const anon = await playwright.request.newContext({ baseURL })
  const denied = await anon.post('/api/rpc/ListUsers', { data: [], headers: JSON_HEADERS })
  expect(denied.status()).toBe(401)
  const caps = await anon.post('/api/rpc/GetCapabilities', { data: [], headers: JSON_HEADERS })
  expect(caps.status()).toBe(200)
  expect((await caps.json()).result).toMatchObject({ fileDialog: false, localKubeconfig: false })
  const status = await anon.post('/api/rpc/AuthStatus', { data: [], headers: JSON_HEADERS })
  expect((await status.json()).result.user).toBeNull() // аноним не видит вошедшего пользователя
  await anon.dispose()

  // Вход, затем вызов без CSRF-токена и с ним
  await page.goto('/')
  const login = await rpc(page, 'Login', [USER, PASSWORD])
  expect(login.status()).toBe(200)
  const csrf = login.headers()['x-csrf-token']
  expect(csrf).toBeTruthy()
  expect((await rpc(page, 'ListUsers', [])).status()).toBe(403)
  const ok = await rpc(page, 'ListUsers', [], csrf)
  expect(ok.status()).toBe(200)
  expect(JSON.stringify(await ok.json())).toContain(USER)

  // Запрос с чужого источника отклоняется даже с верным токеном
  const evil = await page.request.post('/api/rpc/ListUsers', {
    data: [], headers: { ...JSON_HEADERS, 'X-CSRF-Token': csrf, Origin: 'https://evil.example' },
  })
  expect(evil.status()).toBe(403)

  // Методы, недоступные в вебе, закрыты
  const denied2 = await rpc(page, 'PickKubeconfigFile', [], csrf)
  expect(denied2.status()).toBe(403)

  // Выход закрывает сессию
  expect((await rpc(page, 'Logout', [], csrf)).status()).toBe(200)
  expect((await rpc(page, 'ListUsers', [], csrf)).status()).toBe(401)
})

test('кластер: добавление kubeconfig текстом и чтение namespace через сервер', async ({ page }) => {
  const file = process.env.E2E_KUBECONFIG
  test.skip(!file, 'E2E_KUBECONFIG не задан (нужен kubeconfig доступного кластера, например kind)')
  const kubeconfig = readFileSync(file!, 'utf8')

  await logIn(page)
  await page.locator('.welcome-screen').getByRole('button', { name: 'Добавить кластер' }).click()
  await page.getByPlaceholder('Например, Production EU').fill('e2e-kind')
  await page.getByLabel('Текст kubeconfig').fill(kubeconfig)
  const submit = page.locator('.cl-foot .cl-btn-primary')
  await expect(submit).toBeEnabled() // kubeconfig разобран сервером
  await submit.click()
  await expect(page.locator('.cl-dialog')).toHaveCount(0)

  const csrf = await csrfOf(page)
  const list = await (await rpc(page, 'GetClusterStatuses', [], csrf)).json()
  const cluster = list.result.find((c: { displayName: string }) => c.displayName === 'e2e-kind')
  expect(cluster, 'добавленный кластер в списке').toBeTruthy()

  const connect = await rpc(page, 'ConnectCluster', [cluster.id], csrf)
  expect((await connect.json()).error).toBeUndefined()
  const ns = await (await rpc(page, 'GetNamespaces', [], csrf)).json()
  expect(JSON.stringify(ns.result)).toContain('kube-system')
})

test('терминал: OpenTerminal и бинарный WebSocket выполняют команду в поде', async ({ page }) => {
  const kubeconfig = process.env.E2E_KUBECONFIG
  const target = process.env.E2E_TERMINAL_POD // namespace/pod/container, в образе есть /bin/sh
  test.skip(!kubeconfig || !target, 'нужны E2E_KUBECONFIG и E2E_TERMINAL_POD (namespace/pod/container)')
  const [namespace, pod, container] = target!.split('/')

  await page.goto('/')
  const login = await rpc(page, 'Login', [USER, PASSWORD])
  const csrf = login.headers()['x-csrf-token']
  const list = await (await rpc(page, 'GetClusterStatuses', [], csrf)).json()
  const cluster = list.result.find((c: { displayName: string }) => c.displayName === 'e2e-kind')
  expect(cluster, 'кластер добавлен предыдущим тестом').toBeTruthy()
  expect((await (await rpc(page, 'ConnectCluster', [cluster.id], csrf)).json()).error).toBeUndefined()

  // Старые методы с id от клиента закрыты в вебе
  expect((await rpc(page, 'StartTerminalSession', ['mine', namespace, pod, container, '/bin/sh', 80, 24], csrf)).status()).toBe(403)

  const open = await (await rpc(page, 'OpenTerminal', [namespace, pod, container, '/bin/sh', 80, 24], csrf)).json()
  expect(open.error, JSON.stringify(open.error)).toBeUndefined()
  const id: string = open.result
  expect(id).toMatch(/^([\w-]+\.)?[0-9a-f]{32}$/) // с префиксом реплики-владельца (ADR-0016)

  // Команда выполняется в поде; результат вычислен оболочкой (в эхо ввода его нет)
  const output = await page.evaluate(
    ({ id }) => new Promise<string>((resolve, reject) => {
      const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/terminal/${id}`)
      ws.binaryType = 'arraybuffer'
      let out = ''
      const timer = setTimeout(() => reject(new Error('нет вывода команды: ' + out)), 20000)
      ws.onopen = () => ws.send(new TextEncoder().encode('echo kubron-$((20+22))\n'))
      ws.onmessage = e => {
        out += new TextDecoder().decode(e.data as ArrayBuffer)
        if (out.includes('kubron-42')) { clearTimeout(timer); ws.close(1000); resolve(out) }
      }
      ws.onerror = () => reject(new Error('ошибка WebSocket'))
    }),
    { id },
  )
  expect(output).toContain('kubron-42')

  await rpc(page, 'CloseTerminal', [id], csrf)
})
