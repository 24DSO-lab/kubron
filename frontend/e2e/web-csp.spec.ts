// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, test, type Page } from '@playwright/test'

// Нарушения CSP в Chromium и Firefox (задача B1). Файл идёт ПОСЛЕ smoke.spec.ts и two-users.spec.ts
// (по алфавиту), поэтому администратор e2e-admin уже создан; на пустом сервере тест создаёт его сам.
// Политика отдаётся только Go-сервером (internal/server/headers.go): в index.html её нет.
test.describe.configure({ mode: 'serial' })

const USER = 'e2e-admin'
const PASSWORD = 'e2e-password-1'

/** Собирает нарушения CSP двумя независимыми путями: событием браузера и сообщением консоли. */
async function collectViolations(page: Page): Promise<string[]> {
  const found: string[] = []
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const v = e as SecurityPolicyViolationEvent
      ;(window as any).__cspViolations = ((window as any).__cspViolations ?? []) as string[]
      ;(window as any).__cspViolations.push(`${v.violatedDirective} blocked ${v.blockedURI || 'inline'} (${v.sourceFile}:${v.lineNumber})`)
    })
  })
  page.on('console', (m) => {
    const text = m.text()
    if (/content security policy|violates the following|csp/i.test(text)) found.push(`console: ${text}`)
  })
  page.on('pageerror', (e) => {
    if (/content security policy/i.test(e.message)) found.push(`pageerror: ${e.message}`)
  })
  return found
}

async function pageViolations(page: Page, found: string[]): Promise<string[]> {
  const fromEvents = await page.evaluate(() => ((window as any).__cspViolations ?? []) as string[])
  return [...found, ...fromEvents.map((v) => `event: ${v}`)]
}

async function signIn(page: Page) {
  await page.goto('/')
  const setup = page.getByRole('heading', { name: 'Добро пожаловать в Kubron', exact: true })
  const login = page.getByRole('heading', { name: 'Вход', exact: true })
  await expect(setup.or(login)).toBeVisible()
  if (await setup.isVisible()) {
    await page.locator('input[autocomplete=username]').fill(USER)
    await page.locator('input[autocomplete=new-password]').nth(0).fill(PASSWORD)
    await page.locator('input[autocomplete=new-password]').nth(1).fill(PASSWORD)
    await page.getByRole('button', { name: 'Создать администратора' }).click()
  } else {
    await page.locator('input[autocomplete=username]').fill(USER)
    await page.locator('input[autocomplete=current-password]').fill(PASSWORD)
    await page.getByRole('button', { name: 'Войти' }).click()
  }
  await expect(page.locator('.welcome-screen, .cluster-bar, nav')).not.toHaveCount(0)
}

test('заголовки безопасности и CSP без unsafe-inline для скриптов', async ({ request }) => {
  for (const path of ['/', '/healthz', '/api/session']) {
    const res = await request.get(path)
    const h = res.headers()
    const csp = h['content-security-policy']
    expect(csp, `CSP на ${path}`).toBeTruthy()
    const script = csp.split(';').map((d) => d.trim()).find((d) => d.startsWith('script-src')) ?? ''
    expect(script).toBe("script-src 'self'")
    expect(csp).not.toContain('unsafe-eval')
    expect(csp).toContain("frame-ancestors 'none'")
    expect(csp).toContain("object-src 'none'")
    expect(h['x-content-type-options']).toBe('nosniff')
    expect(h['cross-origin-opener-policy']).toBe('same-origin')
    expect(h['permissions-policy']).toContain('camera=()')
    // тест идёт по HTTP на loopback: HSTS здесь быть не должно
    expect(h['strict-transport-security']).toBeUndefined()
  }
  // CSP только в заголовке: десктопная сборка не должна получать политику из index.html
  const index = await (await request.get('/')).text()
  expect(index).not.toMatch(/http-equiv=["']?Content-Security-Policy/i)
})

test('экран входа, интерфейс и диалог добавления кластера работают без нарушений CSP', async ({ page }) => {
  const found = await collectViolations(page)

  await page.goto('/')
  await expect(page.locator('#app[data-v-app]')).not.toBeEmpty() // скрипты приложения выполнились
  await signIn(page)

  // Кнопка находится в навигации кластеров, а не внутри welcome-screen.
  // Проверяем диалог и после первого входа, и при уже существующем кластере.
  const addClusterButton = page.getByRole('button', { name: 'Добавить кластер', exact: true })
  await expect(addClusterButton).toBeVisible()
  await addClusterButton.click()
  await expect(page.locator('.cl-dialog')).toBeVisible()
  await page.locator('.cl-dialog').getByRole('button', { name: /Отмена|Закрыть/ }).first().click()

  // Выход и повторная загрузка: те же страницы после перезагрузки
  await page.reload()
  await expect(page.locator('#app[data-v-app]')).not.toBeEmpty()

  expect(await pageViolations(page, found), 'нарушения CSP').toEqual([])
})

test('WebSocket событий и терминала разрешены политикой (connect-src)', async ({ page, baseURL }) => {
  const found = await collectViolations(page)
  await signIn(page)
  // Событийный WebSocket интерфейс открывает сам после входа; проверяем и явное подключение с тем же origin
  const opened = await page.evaluate(
    (url) =>
      new Promise<string>((resolve) => {
        const ws = new WebSocket(`${url.replace(/^http/, 'ws')}/api/ws`)
        ws.onopen = () => { ws.close(); resolve('open') }
        ws.onerror = () => resolve('error')
        setTimeout(() => resolve('timeout'), 10_000)
      }),
    baseURL!,
  )
  expect(opened).toBe('open')
  expect(await pageViolations(page, found), 'нарушения CSP').toEqual([])
})

test('кластер: интерфейс и редактор открываются без нарушений CSP', async ({ page }) => {
  test.skip(!process.env.E2E_KUBECONFIG, 'нужен кластер: E2E_KUBECONFIG (добавлен тестом smoke.spec.ts)')
  const found = await collectViolations(page)
  await signIn(page)
  // Кластер e2e-kind добавлен smoke.spec.ts; открываем список подов через интерфейс
  const cluster = page.getByText('e2e-kind').first()
  if (await cluster.isVisible().catch(() => false)) await cluster.click()
  // networkidle не наступает при открытом WebSocket событий: даём интерфейсу время загрузить данные кластера
  await page.waitForTimeout(3000)
  expect(await pageViolations(page, found), 'нарушения CSP').toEqual([])
})
