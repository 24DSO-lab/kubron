// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type Page } from '@playwright/test'

// Идёт после smoke.spec.ts (файлы выполняются по алфавиту): администратор e2e уже создан.
// Элементы — по data-testid и ролям, не по русским надписям.
test.describe.configure({ mode: 'serial' })

const USER = 'e2e-admin'
const PASSWORD = 'e2e-password-1'
const JSON_HEADERS = { 'Content-Type': 'application/json' }

async function rpc(page: Page, method: string, args: unknown[], csrf?: string): Promise<APIResponse> {
  return page.request.post(`/api/rpc/${method}`, { data: args, headers: csrf ? { ...JSON_HEADERS, 'X-CSRF-Token': csrf } : JSON_HEADERS })
}

async function login(page: Page): Promise<string> {
  await page.goto('/')
  const res = await rpc(page, 'Login', [USER, PASSWORD])
  expect(res.status()).toBe(200)
  return res.headers()['x-csrf-token']
}

const INCIDENT_METHODS = [
  'IncidentOpen', 'IncidentRefresh', 'IncidentGet', 'IncidentList', 'IncidentMarkOutcome', 'IncidentSaveFragment',
  'IncidentDeleteFragment', 'IncidentSetLogCapture', 'IncidentReadFragment', 'IncidentExport', 'IncidentDelete',
  'IncidentGetSettings', 'IncidentSaveSettings',
]

test('RPC расследований закрыты без сессии и без CSRF-токена', async ({ page, playwright, baseURL }) => {
  const anon = await playwright.request.newContext({ baseURL })
  for (const m of INCIDENT_METHODS) {
    const res = await anon.post(`/api/rpc/${m}`, { data: [], headers: JSON_HEADERS })
    expect(res.status(), m).toBe(401)
  }
  await anon.dispose()

  const csrf = await login(page)
  expect((await rpc(page, 'IncidentList', ['no-such-cluster', {}])).status()).toBe(403) // нет CSRF
  const res = await rpc(page, 'IncidentList', ['no-such-cluster', {}], csrf)
  // Чужой или несуществующий кластер — ошибка доступа, а не пустая история и не 500.
  expect(res.status()).not.toBe(500)
  expect(JSON.stringify(await res.json())).not.toContain('"items"')
})

test('расследование workload: контекст, findings, гипотезы, рекомендации, результат', async ({ page }) => {
  const file = process.env.E2E_KUBECONFIG
  const workload = process.env.E2E_INCIDENT_WORKLOAD // например "Deployment/default/broken-app"
  test.skip(!file || !workload, 'нужны E2E_KUBECONFIG и E2E_INCIDENT_WORKLOAD (вид/namespace/имя падающего workload)')
  const [kind, namespace, name] = workload!.split('/')
  readFileSync(file!, 'utf8') // кластер добавлен тестом smoke.spec.ts

  await login(page)
  await page.goto('/')
  await page.getByTestId('nav-incidents').click()
  await page.getByTestId('open-kind').selectOption(kind)
  await page.getByTestId('open-namespace').fill(namespace)
  await page.getByTestId('open-name').fill(name)
  await page.getByTestId('open-submit').click()

  await expect(page.getByTestId('incident-detail')).toBeVisible()
  await expect(page.getByTestId('findings')).toBeVisible()
  await expect(page.getByTestId('fact').first()).toHaveAttribute('data-kind', 'fact')
  // Гипотеза не выдаётся за установленную причину
  await expect(page.getByTestId('hypothesis-note')).toBeVisible()
  await expect(page.getByTestId('hypothesis-confirmed')).toHaveCount(0)

  await page.getByTestId('outcome-result').selectOption('resolved')
  await page.getByTestId('outcome-submit').click()
  await expect(page.getByTestId('outcome-done')).toBeVisible()
  await expect(page.getByTestId('detail-status')).toHaveText(/./)

  await page.getByTestId('back').click()
  await expect(page.getByTestId('incident-row').first()).toBeVisible()
})
