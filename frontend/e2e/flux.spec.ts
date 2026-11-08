// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { expect, test, type APIResponse, type Browser, type BrowserContext, type Page } from '@playwright/test'

// Flux CD (ADR-0023, раздел 10): настоящий Flux в kind. Нужны E2E_FLUX=1, E2E_KUBECONFIG кластера с Flux и тестовыми
// объектами и E2E_PF_URL (отдельный сервер с пустым каталогом данных, как у pf-argocd.spec.ts: основной сервер на :8080
// принадлежит smoke.spec.ts, который идёт позже по алфавиту и сам создаёт администратора). Без них набор пропускается.
// В CI объекты создаёт шаг «Flux» (ci.yml), namespace — E2E_FLUX_NAMESPACE (по умолчанию flux-e2e):
//   Kustomization и HelmRelease e2e-podinfo; приостановленная Kustomization e2e-delete — только для теста удаления
//   (после него объекта нет, повторный локальный прогон требует пересоздать его).
// Администратор и роль «operator» создаются здесь; pf-argocd и pf-terminals позже входят под тем же администратором.
// Селекторы: data-testid и роли, без переведённых текстов. Состояние объектов читается и по RPC, чтобы тест не
// зависел от скорости перерисовки.
test.describe.configure({ mode: 'serial' })

const PF_URL = process.env.E2E_PF_URL
const KUBECONFIG = process.env.E2E_KUBECONFIG
const NS = process.env.E2E_FLUX_NAMESPACE || 'flux-e2e'
const ADMIN = { name: 'pf-admin', pass: 'pf-password-1' }
const OPERATOR = { name: 'flux-op', tmp: 'flux-temp-pass-1', pass: 'flux-op-pass-2' }
const JSON_HEADERS = { 'Content-Type': 'application/json' }
const REQUESTED_AT = 'reconcile.fluxcd.io/requestedAt'

test.skip(!process.env.E2E_FLUX || !PF_URL || !KUBECONFIG, 'нужны E2E_FLUX=1, E2E_PF_URL и E2E_KUBECONFIG')
test.use({ baseURL: PF_URL })

interface Client { ctx: BrowserContext; page: Page; csrf: string }

interface FluxObject {
  kind: string
  name: string
  namespace: string
  suspended: boolean
  annotations: Record<string, string>
}

let admin: Client
let operator: Client

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

/** Отдельный контекст браузера: своя cookie-сессия и своё активное подключение кластера */
async function newClient(browser: Browser): Promise<Client> {
  const ctx = await browser.newContext({ baseURL: PF_URL })
  const page = await ctx.newPage()
  await page.goto('/')
  return { ctx, page, csrf: '' }
}

async function connect(c: Client, clusterName: string): Promise<void> {
  const list = await result<{ id: string; displayName: string }[]>(c, 'GetClusterStatuses')
  const cluster = list.find(x => x.displayName === clusterName)
  expect(cluster, `кластер ${clusterName} виден пользователю`).toBeTruthy()
  await result(c, 'ConnectCluster', [cluster!.id])
}

const kustomizations = (c: Client) => result<FluxObject[]>(c, 'GetFluxKustomizations', [[NS]])
const helmReleases = (c: Client) => result<FluxObject[]>(c, 'GetFluxHelmReleases', [[NS]])
const findObject = (list: FluxObject[], name: string) => list.find(o => o.name === name && o.namespace === NS)

/** status.lastHandledReconcileAt читается из YAML объекта: в DTO он не отдаётся (там время условия Ready) */
async function lastHandled(c: Client, plural: string, version: string, group: string, name: string): Promise<string> {
  const yaml = await result<string>(c, 'GetResourceYAML', [NS, `cr:${group}/${version}/${plural}`, name, false])
  return /lastHandledReconcileAt:\s*"?([^"\s]+)"?/.exec(yaml)?.[1] ?? ''
}

const KS_YAML = { group: 'kustomize.toolkit.fluxcd.io', version: 'v1', plural: 'kustomizations' }

const row = (page: Page, name: string) =>
  page.locator(`[data-testid="resource-row"][data-row-key="${NS}/${name}"]`)

/** Открывает список в боковом меню: группа Flux может быть свёрнута */
async function openView(page: Page, view: string): Promise<void> {
  await expect(page.getByTestId('nav-group-flux')).toBeVisible()
  const item = page.getByTestId(`nav-item-${view}`)
  if (!(await item.isVisible())) await page.getByTestId('nav-group-flux').click()
  await item.click()
}

/** Список открыт, объект выбран: справа панель деталей с действиями */
async function selectObject(page: Page, view: string, name: string): Promise<void> {
  await page.goto('/')
  await openView(page, view)
  await expect(row(page, name)).toBeVisible()
  await row(page, name).click()
}

test.beforeAll(async ({ browser }) => {
  admin = await newClient(browser)
  operator = await newClient(browser)
})

test.afterAll(async () => {
  await admin?.ctx.close()
  await operator?.ctx.close()
})

test('подготовка: администратор, кластер с Flux и пользователь с ролью operator', async () => {
  const setup = await admin.ctx.request.post('/api/rpc/SetupAdmin', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  const res = setup.status() === 200 ? setup : await admin.ctx.request.post('/api/rpc/Login', { data: [ADMIN.name, ADMIN.pass], headers: JSON_HEADERS })
  expect(res.status()).toBe(200)
  admin.csrf = res.headers()['x-csrf-token']

  const list = await result<{ id: string; displayName: string }[]>(admin, 'GetClusterStatuses')
  if (!list.some(c => c.displayName === 'flux-kind')) {
    await result(admin, 'AddClusterFromText', [readFileSync(KUBECONFIG!, 'utf8'), { displayName: 'flux-kind' }])
  }
  await connect(admin, 'flux-kind')

  // Роль operator: reconcile (operate) есть, suspend/resume/delete (write) нет
  const users = await result<{ username: string }[]>(admin, 'ListUsers')
  if (!users.some(u => u.username === OPERATOR.name)) {
    await result(admin, 'CreateUser', [OPERATOR.name, OPERATOR.tmp, 'operator'])
  }
  const first = await login(operator, OPERATOR.name, OPERATOR.tmp)
  if (first.status() === 200) {
    await result(operator, 'ChangePassword', [OPERATOR.tmp, OPERATOR.pass])
  } else {
    expect((await login(operator, OPERATOR.name, OPERATOR.pass)).status(), 'повторный запуск: пароль уже сменён').toBe(200)
  }
  await connect(operator, 'flux-kind')
})

test('раздел Flux виден в кластере с Flux', async () => {
  const status = await result<{ installed: boolean; controllers: { kustomize: boolean; helm: boolean } }>(admin, 'GetFluxStatus')
  expect(status.installed).toBe(true)
  expect(status.controllers.kustomize).toBe(true)
  expect(status.controllers.helm).toBe(true)

  await admin.page.goto('/')
  await expect(admin.page.getByTestId('nav-group-flux')).toBeVisible()
})

test('в кластере без Flux раздела нет', async () => {
  // В e2e-кластере Flux есть всегда, поэтому ответ обнаружения подменяется: проверяется, что интерфейс прячет раздел
  // по FluxStatus.installed=false (само обнаружение покрыто Go-тестами)
  const none = { installed: false, state: '', controllers: { kustomize: false, helm: false, source: false, notification: false, image: false }, versions: {} }
  await admin.page.route('**/api/rpc/GetFluxStatus', route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ result: none }),
  }))
  try {
    await admin.page.goto('/')
    await expect(admin.page.locator('nav.nav-tree')).toBeVisible()
    await expect(admin.page.getByTestId('nav-group-flux')).toHaveCount(0)
    await expect(admin.page.getByTestId('nav-item-flux-kustomizations')).toHaveCount(0)
  } finally {
    await admin.page.unroute('**/api/rpc/GetFluxStatus')
  }
})

test('списки Kustomization и HelmRelease содержат тестовые объекты', async () => {
  const ks = await kustomizations(admin)
  expect(findObject(ks, 'e2e-podinfo'), 'Kustomization e2e-podinfo в RPC').toBeTruthy()
  expect(findObject(await helmReleases(admin), 'e2e-podinfo'), 'HelmRelease e2e-podinfo в RPC').toBeTruthy()

  await admin.page.goto('/')
  await openView(admin.page, 'flux-kustomizations')
  await expect(row(admin.page, 'e2e-podinfo')).toBeVisible()
  await openView(admin.page, 'flux-helmreleases')
  await expect(row(admin.page, 'e2e-podinfo')).toBeVisible()
})

test('Reconcile меняет lastHandledReconcileAt', async () => {
  const before = {
    handled: await lastHandled(admin, KS_YAML.plural, KS_YAML.version, KS_YAML.group, 'e2e-podinfo'),
    requested: findObject(await kustomizations(admin), 'e2e-podinfo')?.annotations?.[REQUESTED_AT] ?? '',
  }

  await selectObject(admin.page, 'flux-kustomizations', 'e2e-podinfo')
  await admin.page.getByTestId('resource-action-reconcile').click()

  // Kubron записывает токен запроса сразу, контроллер подтверждает его в status.lastHandledReconcileAt
  await expect.poll(async () => findObject(await kustomizations(admin), 'e2e-podinfo')?.annotations?.[REQUESTED_AT] ?? '', { timeout: 15_000 })
    .not.toBe(before.requested)
  const requested = findObject(await kustomizations(admin), 'e2e-podinfo')!.annotations[REQUESTED_AT]
  expect(requested).toBeTruthy()

  await expect.poll(() => lastHandled(admin, KS_YAML.plural, KS_YAML.version, KS_YAML.group, 'e2e-podinfo'), {
    timeout: 90_000, message: 'контроллер обработал запрос reconcile',
  }).toBe(requested)
  expect(requested).not.toBe(before.handled)
})

test('Suspend, затем Resume меняют признак', async () => {
  await selectObject(admin.page, 'flux-kustomizations', 'e2e-podinfo')
  await expect(admin.page.getByTestId('resource-action-suspend')).toBeVisible()
  await expect(admin.page.getByTestId('resource-action-resume')).toHaveCount(0)

  await admin.page.getByTestId('resource-action-suspend').click()
  await admin.page.getByTestId('confirm-accept').click()
  await expect.poll(async () => findObject(await kustomizations(admin), 'e2e-podinfo')?.suspended, { timeout: 30_000 }).toBe(true)
  await expect(admin.page.getByTestId('resource-action-resume')).toBeVisible()

  await admin.page.getByTestId('resource-action-resume').click()
  await admin.page.getByTestId('confirm-accept').click()
  await expect.poll(async () => findObject(await kustomizations(admin), 'e2e-podinfo')?.suspended, { timeout: 30_000 }).toBe(false)
  await expect(admin.page.getByTestId('resource-action-suspend')).toBeVisible()
})

test('роль без права write не видит Suspend, Resume и Delete', async () => {
  await selectObject(operator.page, 'flux-kustomizations', 'e2e-podinfo')
  // Reconcile (operate) у роли operator есть: панель деталей загрузилась и действия считаются по правам
  await expect(operator.page.getByTestId('resource-action-reconcile')).toBeVisible()
  for (const id of ['suspend', 'resume', 'delete']) {
    await expect(operator.page.getByTestId(`resource-action-${id}`)).toHaveCount(0)
  }
  await expect(operator.page.getByTestId('resource-delete')).toHaveCount(0)

  // Закрываем панель деталей: она перекрывает правую часть списка и перехватывает pointer events
  await operator.page.getByTestId('resource-detail-close').click()

  // То же в контекстном меню строки
  await row(operator.page, 'e2e-podinfo').click({ button: 'right' })
  await expect(operator.page.getByTestId('resource-menu-reconcile')).toBeVisible()
  for (const id of ['suspend', 'resume', 'delete']) {
    await expect(operator.page.getByTestId(`resource-menu-${id}`)).toHaveCount(0)
  }

  // Сервер отказывает независимо от интерфейса
  const ref = { kind: 'Kustomization', namespace: NS, name: 'e2e-podinfo' }
  expect((await call(operator, 'SuspendFlux', [[ref]])).status()).toBe(403)
  expect((await call(operator, 'ResumeFlux', [[ref]])).status()).toBe(403)
  expect((await call(operator, 'DeleteFlux', [ref])).status()).toBe(403)
  expect(findObject(await kustomizations(admin), 'e2e-podinfo')?.suspended).toBe(false)
})

test('Delete: кнопка неактивна, пока имя не введено, после удаления объект исчезает', async () => {
  expect(findObject(await kustomizations(admin), 'e2e-delete'), 'Kustomization e2e-delete создана шагом CI').toBeTruthy()

  await selectObject(admin.page, 'flux-kustomizations', 'e2e-delete')
  await admin.page.getByTestId('resource-action-delete').click()

  const dialog = admin.page.getByTestId('flux-delete-dialog')
  await expect(dialog).toBeVisible()
  // prune: false: ресурсы остаются без управления
  await expect(admin.page.getByTestId('flux-delete-consequence')).toHaveAttribute('data-consequence', 'keep')
  const confirm = admin.page.getByTestId('flux-delete-confirm')
  await expect(confirm).toBeDisabled()
  await admin.page.getByTestId('flux-delete-input').fill('e2e-delet')
  await expect(confirm).toBeDisabled()
  await admin.page.getByTestId('flux-delete-input').fill('e2e-delete')
  await expect(confirm).toBeEnabled()

  await confirm.click()
  await expect(dialog).toHaveCount(0)
  await expect(row(admin.page, 'e2e-delete')).toHaveCount(0)
  await expect.poll(async () => findObject(await kustomizations(admin), 'e2e-delete'), { timeout: 30_000 }).toBeUndefined()
  // Остальные объекты на месте
  expect(findObject(await kustomizations(admin), 'e2e-podinfo')).toBeTruthy()
})
