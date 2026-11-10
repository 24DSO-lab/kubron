// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Приём отчётов «Сообщить об ошибке» из приложения Kubron.
 *
 * Отчёт (текст, снимок экрана, несколько фактов) сохраняется в ПРИВАТНОМ репозитории отчётов: снимок кладётся файлом,
 * по отчёту создаётся issue. Токен GitHub лежит только в секретах Cloudflare, в приложении секретов нет.
 *
 * Код не зависит от рантайма Cloudflare: fetch и часы передаются параметрами, поэтому он проверяется обычным
 * `node --test` (см. bugreport.test.ts). Только синтаксис, который Node умеет «стирать» (без enum и namespace).
 */

export interface KVLike {
  get(key: string): Promise<string | null>
  put(key: string, value: string, options?: { expirationTtl?: number }): Promise<void>
}

export interface Env {
  /** Секрет: fine-grained токен, только репозиторий отчётов, права Contents и Issues: read/write. */
  REPORTS_GITHUB_TOKEN?: string
  /** Приватный репозиторий отчётов, «владелец/имя». В нём должен быть хотя бы один коммит. */
  REPORTS_REPO?: string
  /** Ветка для снимков; по умолчанию main. */
  REPORTS_BRANCH?: string
  /** KV для лимитов. Без него приём закрыт: анонимный адрес без лимитов не открываем. */
  RATE?: KVLike
}

export interface Deps {
  fetch: typeof fetch
  now: () => Date
}

// Пределы: описание и снимок те же, что проверяет приложение (internal/bugreport/report.go).
export const MAX_DESCRIPTION = 5000
export const MAX_SCREENSHOT_BYTES = 6 << 20
export const MAX_BODY_BYTES = 9 << 20 // снимок в base64 плюс JSON
const MAX_SIDE = 16384
const MAX_FIELD = 64
const MAX_CONTACT = 254

/** Лимиты. Счётчики в KV приблизительные (нет атомарного прироста), для защиты от потока этого достаточно. */
export const LIMIT_PER_IP_HOUR = 10
export const LIMIT_PER_DAY = 200

const ID_RE = /^[0-9a-f]{32}$/
const REPO_RE = /^[A-Za-z0-9_.-]{1,100}\/[A-Za-z0-9_.-]{1,100}$/
const BRANCH_RE = /^[A-Za-z0-9_./-]{1,100}$/
const B64_RE = /^[A-Za-z0-9+/]*={0,2}$/
const CONTACT_RE = /^[^\s@<>"]+@[^\s@<>"]+\.[^\s@<>"]+$/
const PNG_SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]

export interface Diagnostics {
  appVersion: string
  shell: string
  os: string
  arch: string
  locale: string
  view: string
}

export interface Report {
  id: string
  description: string
  contact: string
  screenshot: string // PNG в base64; пусто — без снимка
  screenshotBytes: number
  diagnostics: Diagnostics
}

/** Ошибка с кодом ответа. Текст фиксированный: он уходит клиенту, значения из запроса в него не попадают. */
export class HttpError extends Error {
  status: number
  headers: Record<string, string>
  constructor(status: number, message: string, headers: Record<string, string> = {}) {
    super(message)
    this.status = status
    this.headers = headers
  }
}

function json(status: number, body: Record<string, unknown>, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store', ...headers },
  })
}

/** Читает тело не больше max байт, не доверяя Content-Length (его может не быть или он может лгать). */
export async function readLimited(request: Request, max: number): Promise<Uint8Array> {
  const declared = Number(request.headers.get('content-length') ?? '')
  if (Number.isFinite(declared) && declared > max) throw new HttpError(413, 'the report is too large')
  if (!request.body) throw new HttpError(400, 'the request has no body')
  const reader = request.body.getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    total += value.byteLength
    if (total > max) {
      await reader.cancel()
      throw new HttpError(413, 'the report is too large')
    }
    chunks.push(value)
  }
  const out = new Uint8Array(total)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.byteLength
  }
  return out
}

/** Убирает управляющие символы и недопустимые суррогаты; переводы строк и табуляция остаются. */
export function cleanText(s: string): string {
  return s
    .toWellFormed()
    .replace(/\r/g, '')
    // eslint-disable-next-line no-control-regex
    .replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g, '')
}

/** Одна строка без управляющих символов, не длиннее max символов (по кодовым точкам). */
export function oneLine(s: string, max: number): string {
  const line = cleanText(s).split(/\s+/).filter(Boolean).join(' ')
  return Array.from(line).slice(0, max).join('')
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/** Проверяет присланное так же строго, как приложение перед отправкой; всё лишнее отбрасывается. */
export function parseReport(raw: unknown): Report {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) throw new HttpError(400, 'the report is not valid')
  const o = raw as Record<string, unknown>

  const id = str(o.id)
  if (!ID_RE.test(id)) throw new HttpError(400, 'the report id is not valid')

  const description = cleanText(str(o.description)).trim()
  if (description === '') throw new HttpError(400, 'the description is required')
  if (Array.from(description).length > MAX_DESCRIPTION) throw new HttpError(400, 'the description is too long')

  let contact = str(o.contact).trim()
  if (contact !== '') {
    if (contact.length > MAX_CONTACT || !CONTACT_RE.test(contact)) throw new HttpError(400, 'the contact is not valid')
  } else {
    contact = ''
  }

  const screenshot = str(o.screenshot)
  let screenshotBytes = 0
  if (screenshot !== '') screenshotBytes = checkPng(screenshot)

  const d = typeof o.diagnostics === 'object' && o.diagnostics !== null ? (o.diagnostics as Record<string, unknown>) : {}
  const diagnostics: Diagnostics = {
    appVersion: oneLine(str(d.appVersion), MAX_FIELD),
    shell: oneLine(str(d.shell), MAX_FIELD),
    os: oneLine(str(d.os), MAX_FIELD),
    arch: oneLine(str(d.arch), MAX_FIELD),
    locale: oneLine(str(d.locale), MAX_FIELD),
    view: oneLine(str(d.view), MAX_FIELD),
  }
  return { id, description, contact, screenshot, screenshotBytes, diagnostics }
}

/** Проверяет base64 и заголовок PNG (подпись и размеры из IHDR) и возвращает размер файла. */
function checkPng(b64: string): number {
  if (b64.length % 4 !== 0 || !B64_RE.test(b64)) throw new HttpError(400, 'the screenshot is not valid')
  const pad = b64.endsWith('==') ? 2 : b64.endsWith('=') ? 1 : 0
  const size = (b64.length / 4) * 3 - pad
  if (size > MAX_SCREENSHOT_BYTES) throw new HttpError(413, 'the screenshot is too large')
  if (size < 33) throw new HttpError(400, 'the screenshot is not valid')
  const head = atob(b64.slice(0, 44)) // 33 байта заголовка: подпись, длина и тип IHDR, ширина, высота
  const b = Array.from(head, (c) => c.charCodeAt(0))
  if (!PNG_SIGNATURE.every((v, i) => b[i] === v) || head.slice(12, 16) !== 'IHDR') {
    throw new HttpError(400, 'the screenshot is not valid')
  }
  const w = ((b[16] << 24) | (b[17] << 16) | (b[18] << 8) | b[19]) >>> 0
  const h = ((b[20] << 24) | (b[21] << 16) | (b[22] << 8) | b[23]) >>> 0
  if (w === 0 || h === 0 || w > MAX_SIDE || h > MAX_SIDE) throw new HttpError(400, 'the screenshot is not valid')
  return size
}

// ---------------------------------------------------------------------------------------------------------------------
// Вид issue

/** Ограждение из обратных кавычек, которое длиннее любой серии кавычек внутри текста: текст не выйдет из блока. */
export function fence(text: string): string {
  let longest = 0
  for (const m of text.matchAll(/`+/g)) longest = Math.max(longest, m[0].length)
  return '`'.repeat(Math.max(3, longest + 1))
}

/**
 * Заголовок: первая строка описания в одну строку. Заголовок не в блоке кода, поэтому «@» и «#» убираются: «#12» и
 * «владелец/репо#12» стали бы перекрёстными ссылками на чужие issue.
 */
export function issueTitle(description: string): string {
  const first = description.split('\n', 1)[0].replace(/[@#]/g, '')
  return '[bug] ' + (oneLine(first, 80) || '(no title)')
}

/**
 * Тело issue. Текст пользователя лежит в блоке кода: в нём не работают разметка, ссылки, @упоминания и #номера, поэтому
 * чужой текст не может никого уведомить и не может исказить страницу.
 */
export function issueBody(r: Report, repo: string, branch: string, now: Date): string {
  const f = fence(r.description)
  const d = r.diagnostics
  const dash = (s: string) => (s === '' ? '-' : s)
  const lines = [
    f,
    r.description,
    f,
    '',
    '| | |',
    '|---|---|',
    `| Application | Kubron ${dash(d.appVersion)} (${dash(d.shell)}) |`,
    `| System | ${dash(d.os)}/${dash(d.arch)} |`,
    `| Language | ${dash(d.locale)} |`,
    `| Open section | ${dash(d.view)} |`,
    `| Contact | ${r.contact === '' ? 'not given' : '`' + r.contact + '`'} |`,
    `| Report id | \`${r.id}\` |`,
    `| Received | ${now.toISOString()} |`,
  ]
  if (r.screenshot !== '') {
    const path = screenshotPath(r.id)
    lines.push('', `![screenshot](https://github.com/${repo}/blob/${branch}/${path}?raw=true)`)
    lines.push('', `Screenshot: \`${path}\` (${Math.ceil(r.screenshotBytes / 1024)} KB), visible to those who can open this repository.`)
  } else {
    lines.push('', 'Screenshot: not attached')
  }
  return lines.join('\n')
}

export const screenshotPath = (id: string) => `reports/${id}/screenshot.png`
export const markerPath = (id: string) => `reports/${id}/report.json`

// ---------------------------------------------------------------------------------------------------------------------
// Лимиты

async function limit(kv: KVLike, key: string, max: number, ttl: number): Promise<boolean> {
  const n = Number((await kv.get(key)) ?? '0') || 0
  if (n >= max) return false
  await kv.put(key, String(n + 1), { expirationTtl: ttl })
  return true
}

/** Проверяет и учитывает отчёт: на адрес в час и на всех в сутки. Отказ — 429 с Retry-After. */
export async function takeQuota(kv: KVLike, ip: string, now: Date): Promise<void> {
  const hour = now.toISOString().slice(0, 13)
  const day = now.toISOString().slice(0, 10)
  const who = ip === '' ? 'unknown' : ip
  if (!(await limit(kv, `ip:${who}:${hour}`, LIMIT_PER_IP_HOUR, 2 * 3600))) {
    throw new HttpError(429, 'too many reports from this address; try again later', { 'retry-after': '3600' })
  }
  if (!(await limit(kv, `all:${day}`, LIMIT_PER_DAY, 2 * 86400))) {
    throw new HttpError(429, 'the daily limit of reports is reached; try again tomorrow', { 'retry-after': '3600' })
  }
}

// ---------------------------------------------------------------------------------------------------------------------
// GitHub

class GitHub {
  private readonly token: string
  private readonly repo: string
  private readonly branch: string
  private readonly f: typeof fetch

  constructor(token: string, repo: string, branch: string, f: typeof fetch) {
    this.token = token
    this.repo = repo
    this.branch = branch
    this.f = f
  }

  private async call(method: string, path: string, body?: unknown): Promise<Response> {
    const res = await this.f(`https://api.github.com/repos/${this.repo}/${path}`, {
      method,
      headers: {
        authorization: `Bearer ${this.token}`,
        accept: 'application/vnd.github+json',
        'x-github-api-version': '2022-11-28',
        'user-agent': 'kubron-bugreport-relay',
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    return res
  }

  /** true — файл есть. 404 — нет. Любой другой ответ — сбой GitHub (и не повод считать, что файла нет). */
  async exists(path: string): Promise<boolean> {
    const res = await this.call('GET', `contents/${path}?ref=${encodeURIComponent(this.branch)}`)
    await res.body?.cancel()
    if (res.status === 200) return true
    if (res.status === 404) return false
    throw upstream('contents', res.status)
  }

  /** Кладёт файл. 422 значит «уже есть» (повтор после сбоя), это успех. */
  async put(path: string, contentB64: string, message: string): Promise<void> {
    const res = await this.call('PUT', `contents/${path}`, { message, content: contentB64, branch: this.branch })
    await res.body?.cancel()
    if (res.status === 201 || res.status === 200 || res.status === 422) return
    throw upstream('put', res.status)
  }

  async createIssue(title: string, body: string): Promise<number> {
    const res = await this.call('POST', 'issues', { title, body, labels: ['bug-report'] })
    if (res.status !== 201) {
      await res.body?.cancel()
      throw upstream('issue', res.status)
    }
    const j = (await res.json()) as { number?: unknown }
    if (typeof j.number !== 'number') throw upstream('issue', 502)
    return j.number
  }
}

function upstream(step: string, status: number): HttpError {
  // Токен недействителен, права сняты, лимит GitHub: клиент должен оставить отчёт и повторить позже.
  console.error(`bugreport relay: GitHub ${step} answered ${status}`)
  return new HttpError(502, 'the report could not be stored; try again later')
}

const b64 = (s: string) => {
  const bytes = new TextEncoder().encode(s)
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin)
}

// ---------------------------------------------------------------------------------------------------------------------
// Обработчик

/** Принимает запрос приложения. Возвращает Response всегда: исключения превращаются в ответы с кодом. */
export async function handleReport(request: Request, env: Env, deps: Deps = { fetch: globalThis.fetch.bind(globalThis), now: () => new Date() }): Promise<Response> {
  try {
    return await process(request, env, deps)
  } catch (e) {
    if (e instanceof HttpError) return json(e.status, { error: e.message }, e.headers)
    console.error('bugreport relay: unexpected error', e instanceof Error ? e.name : typeof e)
    return json(500, { error: 'the report could not be processed' })
  }
}

async function process(request: Request, env: Env, deps: Deps): Promise<Response> {
  if (request.method !== 'POST') throw new HttpError(405, 'use POST', { allow: 'POST' })
  if (!(request.headers.get('content-type') ?? '').toLowerCase().startsWith('application/json')) {
    throw new HttpError(415, 'the report must be JSON')
  }

  // Настройка проверяется до чтения тела: не принимаем то, что некуда положить.
  const token = env.REPORTS_GITHUB_TOKEN ?? ''
  const repo = env.REPORTS_REPO ?? ''
  const branch = env.REPORTS_BRANCH || 'main'
  if (token === '' || !REPO_RE.test(repo) || !BRANCH_RE.test(branch) || !env.RATE) {
    console.error('bugreport relay: not configured (token, repository or the RATE namespace is missing)')
    throw new HttpError(503, 'the report receiver is not configured')
  }

  const bytes = await readLimited(request, MAX_BODY_BYTES)
  let raw: unknown
  try {
    raw = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes))
  } catch {
    throw new HttpError(400, 'the report is not valid JSON')
  }
  const report = parseReport(raw)
  const now = deps.now()

  await takeQuota(env.RATE, request.headers.get('cf-connecting-ip') ?? '', now)

  const gh = new GitHub(token, repo, branch, deps.fetch)
  if (await gh.exists(markerPath(report.id))) {
    return json(200, { status: 'duplicate' }) // повтор уже принятого отчёта
  }
  if (report.screenshot !== '') {
    await gh.put(screenshotPath(report.id), report.screenshot, `report ${report.id}: screenshot`)
  }
  const issue = await gh.createIssue(issueTitle(report.description), issueBody(report, repo, branch, now))
  try {
    // Отметка в самом конце: по ней повтор того же отчёта узнаётся и не создаёт второй issue.
    await gh.put(markerPath(report.id), b64(JSON.stringify({ id: report.id, issue, received: now.toISOString() }) + '\n'), `report ${report.id}: issue #${issue}`)
  } catch (e) {
    // Issue уже создан: клиенту отвечаем успехом, иначе он повторит отправку и появится дубль.
    console.error('bugreport relay: the marker of an accepted report was not saved', e instanceof HttpError ? e.status : '')
  }
  return json(201, { status: 'created' })
}
