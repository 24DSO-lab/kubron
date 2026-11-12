// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type BrowserContext, type Page } from '@playwright/test'

// Upgrade Readiness (ADR-0025): проверка настоящего кластера (kind). Нужны E2E_READINESS=1, E2E_KUBECONFIG и E2E_PF_URL
// (отдельный сервер с пустым каталогом данных, как у flux.spec.ts). Без них набор пропускается.
// Что проверяется: отчёт всегда несёт coverage с перечнем «не проверено» (GitOps и локальные файлы), результат попадает
// в историю, второй отчёт сравнивается с первым, экспорт JSON и Markdown содержит раздел coverage, интерфейс показывает
// coverage раньше находок. Селекторы: data-testid, без переведённых текстов.
test.describe.configure({ mode: 'serial' })

const PF_URL = process.env.E2E_PF_URL
const KUBECONFIG = process.env.E2E_KUBECONFIG
const ADMIN = { name: 'pf-admin', pass: 'pf-password-1' }
const JSON_HEADERS = { 'Content-Type': 'application/json' }

test.skip(!process.env.E2E_READINESS || !PF_URL || !KUBECONFIG, 'нужны E2E_READINESS=1, E2E_PF_URL и E2E_KUBECONFIG')
test.use({ baseURL: PF_URL })

interface Source { kind: string; checked: boolean; reason?: { code: string } }
interface Report {
  id: string
  findings: unknown[]
  coverage: { mode: string; sources: Source[]; limitations: { code: string }[] }
}
interface Summary { id: string }
interface Page_ { items: Summary[] }

let ctx: BrowserContext
let page: Page
let csrf = ''
let clusterId = ''

async function call(method: string, args: unknown[] = []): Promise<APIResponse> {
  return ctx.request.post(`/api/rpc/${method}`, { data: args, headers: { ...JSON_HEADERS, 'X-CSRF-Token': csrf } })
}

async function result<T = unknown>(method: string, args: unknown[] = []): Promise<T> {
  const body = await (await call(method, args)).json()
  expect(body.error, `${method}: ${JSON.stringify(body.error)}`).toBeUndefined()
  return body.result as T
}

test.beforeAll(async ({ browser }) => {
  ctx = await browser.newContext({ baseURL: PF_URL })
  page = await ctx.newPage()
  await page.goto('/')
})

test.afterAll(async () => {
  await ctx?.close()
})

test('подготовка: администратор и кластер', async () => {
  const setup = await ctx.request.post('/api/rpc/SetupAdmin', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  const res = setup.status() === 200 ? setup : await ctx.request.post('/api/rpc/Login', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  expect(res.status()).toBe(200)
  csrf = res.headers()['x-csrf-token']

  let list = await result<{ id: string; displayName: string }[]>('GetClusterStatuses')
  if (!list.some(c => c.displayName === 'readiness-kind')) {
    await result('AddClusterFromText', [readFileSync(KUBECONFIG!, 'utf8'), { displayName: 'readiness-kind' }])
    list = await result<{ id: string; displayName: string }[]>('GetClusterStatuses')
  }
  clusterId = list.find(c => c.displayName === 'readiness-kind')!.id
  await result('ConnectCluster', [clusterId])
})

test('отчёт несёт coverage: GitOps и локальные файлы названы «не проверено»', async () => {
  const report = await result<Report>('ReadinessRun', [clusterId, ''])
  expect(report.coverage.mode).toBe('generic-kubernetes')
  const byKind = Object.fromEntries(report.coverage.sources.map(s => [s.kind, s]))
  expect(byKind.live_api.checked).toBe(true)
  for (const k of ['gitops_manifests', 'local_files']) {
    expect(byKind[k].checked, k).toBe(false)
    expect(byKind[k].reason?.code, k).toBeTruthy()
  }
  expect(report.coverage.limitations.map(l => l.code)).toContain('coverage.provider.notChecked')
})

test('второй отчёт попадает в историю и сравнивается с первым', async () => {
  const second = await result<Report>('ReadinessRun', [clusterId, ''])
  const history = await result<Page_>('ReadinessHistory', [clusterId, '', 10])
  expect(history.items.length).toBeGreaterThanOrEqual(2)
  expect(history.items[0].id).toBe(second.id)
  const diff = await result<{ currentId: string; previousId: string }>('ReadinessCompare', [clusterId, second.id, ''])
  expect(diff.currentId).toBe(second.id)
  expect(diff.previousId).toBe(history.items[1].id)
})

test('экспорт JSON и Markdown содержит раздел coverage', async () => {
  const history = await result<Page_>('ReadinessHistory', [clusterId, '', 1])
  const id = history.items[0].id
  const json = await result<{ content: string }>('ReadinessExport', [clusterId, id, 'json'])
  expect(JSON.parse(json.content).report.coverage.sources.length).toBe(3)
  const md = await result<{ content: string }>('ReadinessExport', [clusterId, id, 'markdown'])
  expect(md.content).toMatch(/coverage/i)
})

test('интерфейс: coverage идёт раньше находок', async () => {
  await page.goto('/')
  const item = page.getByTestId('nav-item-readiness')
  if (!(await item.isVisible())) await page.getByTestId('nav-group-config').click()
  await item.click()
  await page.getByTestId('readiness-run').click()
  const coverage = page.getByTestId('readiness-coverage')
  await expect(coverage).toBeVisible()
  await expect(page.getByTestId('readiness-notchecked')).toBeVisible()
  const first = await page.locator('[data-testid="readiness-coverage"], [data-testid^="readiness-group-"]').first().getAttribute('data-testid')
  expect(first).toBe('readiness-coverage')
})
