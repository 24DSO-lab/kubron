// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type Page } from '@playwright/test'

// Этап 5: настоящий Argo CD за прокси потоков port-forward (ADR-0017): UI, API, вход и SSE через origin потока.
// Нужны E2E_PF_URL (сервер с KUBRON_PF_BASE_DOMAIN=localhost:<порт>), E2E_KUBECONFIG кластера с Argo CD в namespace
// argocd и E2E_ARGO_PASSWORD или E2E_ARGO_PASSWORD_FILE (пароль admin из argocd-initial-admin-secret). Без них набор пропускается.
// Идёт до pf-terminals.spec.ts (по алфавиту) и сам создаёт администратора и кластер на пустом сервере.
test.describe.configure({ mode: 'serial' })

const PF_URL = process.env.E2E_PF_URL
const KUBECONFIG = process.env.E2E_KUBECONFIG
// Пароль admin: прямо в переменной или в файле (в CI его пишет шаг «Argo CD»)
const ARGO_PASSWORD = process.env.E2E_ARGO_PASSWORD ??
  (process.env.E2E_ARGO_PASSWORD_FILE ? readFileSync(process.env.E2E_ARGO_PASSWORD_FILE, 'utf8').trim() : undefined)
const ADMIN = { name: 'pf-admin', pass: 'pf-password-1' }
const JSON_HEADERS = { 'Content-Type': 'application/json' }

test.skip(!PF_URL || !KUBECONFIG || !ARGO_PASSWORD, 'нужны E2E_PF_URL, E2E_KUBECONFIG и E2E_ARGO_PASSWORD(_FILE)')

let csrf = ''
let streamId = ''
let streamUrl = ''

test.use({ baseURL: PF_URL })

async function rpc(page: Page, method: string, args: unknown[] = []): Promise<APIResponse> {
  return page.request.post(`/api/rpc/${method}`, { data: args, headers: { ...JSON_HEADERS, 'X-CSRF-Token': csrf } })
}

async function result<T = unknown>(page: Page, method: string, args: unknown[] = []): Promise<T> {
  const body = await (await rpc(page, method, args)).json()
  expect(body.error, `${method}: ${JSON.stringify(body.error)}`).toBeUndefined()
  return body.result as T
}

test('Argo CD открывается через прокси потока: UI, API, вход и SSE, порт на хосте не открывается', async ({ page }) => {
  await page.goto('/')
  const setup = await page.request.post('/api/rpc/SetupAdmin', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  const res = setup.status() === 200
    ? setup
    : await page.request.post('/api/rpc/Login', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  expect(res.status()).toBe(200)
  csrf = res.headers()['x-csrf-token']

  const list = await result<{ displayName: string; id: string }[]>(page, 'GetClusterStatuses')
  let id = list.find(c => c.displayName === 'argo-kind')?.id
  if (!id) {
    id = (await result<{ id: string }>(page, 'AddClusterFromText', [readFileSync(KUBECONFIG!, 'utf8'), { displayName: 'argo-kind' }])).id
  }
  await result(page, 'ConnectToCluster', [id])

  const info = await result<{ url: string; mode: string; localPort: number; portForwardId: string; service: string }>(page, 'OpenArgoUI')
  expect(info.mode).toBe('proxy')
  expect(info.localPort).toBe(0)
  expect(info.service).toBe('argocd-server')
  streamId = info.portForwardId
  streamUrl = info.url

  // Argo CD отвечает по https с самоподписанным сертификатом: прокси сам ходит к нему внутри туннеля
  const ui = await page.goto(streamUrl)
  expect(ui!.status()).toBe(200)
  await expect(page).toHaveTitle(/Argo CD/)

  // Дальше та же страница: адрес входа одноразовый, дальше работает cookie потока (kpf)
  const version = await page.evaluate(async () => {
    const r = await fetch('/api/version')
    return { status: r.status, body: await r.json() }
  })
  expect(version.status).toBe(200)
  expect(version.body.Version).toMatch(/^v\d/)

  const login = await page.evaluate(async (password) => {
    const r = await fetch('/api/v1/session', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password }),
    })
    return { status: r.status, token: (await r.json()).token as string }
  }, ARGO_PASSWORD!)
  expect(login.status).toBe(200)
  expect(login.token).toBeTruthy()

  // Токен принят, а запрос идёт через ту же сессию потока: список приложений отвечает 200
  const apps = await page.evaluate(async (token) => {
    const r = await fetch('/api/v1/applications', { headers: { Authorization: `Bearer ${token}` } })
    return r.status
  }, login.token)
  expect(apps).toBe(200)

  // Поток событий (SSE) отдаётся без буферизации: заголовки приходят, не дожидаясь конца ответа
  const sse = await page.evaluate(async (token) => {
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), 30000)
    try {
      const r = await fetch('/api/v1/stream/applications', { headers: { Authorization: `Bearer ${token}` }, signal: ctl.signal })
      return { status: r.status, type: r.headers.get('content-type') ?? '' }
    } finally {
      clearTimeout(timer)
      ctl.abort()
    }
  }, login.token)
  expect(sse.status).toBe(200)
  expect(sse.type).toContain('text/event-stream')
})

test('остановка потока закрывает доступ', async ({ page }) => {
  await page.goto('/')
  const login = await page.request.post('/api/rpc/Login', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  expect(login.status()).toBe(200)
  csrf = login.headers()['x-csrf-token']
  await result(page, 'StopPortForward', [streamId])
  const stopped = await page.request.get(streamUrl.replace(/\?.*$/, ''), { failOnStatusCode: false }).catch(() => null)
  if (stopped) expect(stopped.status()).not.toBe(200)
  const again = await (await rpc(page, 'GetPortForwardURL', [streamId])).json()
  expect(again.error).toBeTruthy()
})
