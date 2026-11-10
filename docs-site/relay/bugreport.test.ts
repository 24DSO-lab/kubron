// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Запуск: npm run test:relay (node --test, без зависимостей).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  LIMIT_PER_DAY, LIMIT_PER_IP_HOUR, MAX_DESCRIPTION, MAX_SCREENSHOT_BYTES, fence, handleReport, issueTitle, markerPath,
  oneLine, parseReport, readLimited, screenshotPath, type Env, type KVLike,
} from './bugreport.ts'

const ID = '0123456789abcdef0123456789abcdef'
const NOW = new Date('2026-11-10T08:00:00Z')
const RLO = String.fromCharCode(0x202e) // right-to-left override: must never reach an issue

/** PNG из IHDR с нужными размерами: для проверки заголовка остального не нужно. */
function pngB64(w = 4, h = 4, extra = 0): string {
  const b = new Uint8Array(33 + extra)
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52])
  new DataView(b.buffer).setUint32(16, w)
  new DataView(b.buffer).setUint32(20, h)
  return Buffer.from(b).toString('base64')
}

class MemoryKV implements KVLike {
  data = new Map<string, string>()
  async get(k: string) { return this.data.get(k) ?? null }
  async put(k: string, v: string) { this.data.set(k, v) }
}

interface Call { method: string; path: string; body: any; auth: string }

/** GitHub в памяти: файлы по пути и список issue. fail — принудительный ответ для шага. */
function fakeGitHub(opts: { fail?: Record<string, number>; files?: Set<string> } = {}) {
  const files = opts.files ?? new Set<string>()
  const issues: any[] = []
  const calls: Call[] = []
  const f = (async (url: string | URL | Request, init?: RequestInit) => {
    const u = new URL(String(url))
    const path = u.pathname.replace(/^\/repos\/[^/]+\/[^/]+\//, '')
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    const auth = new Headers(init?.headers).get('authorization') ?? ''
    calls.push({ method: init?.method ?? 'GET', path, body, auth })
    const key = `${init?.method} ${path.startsWith('contents/reports/') ? 'contents' : path}`
    const forced = opts.fail?.[key] ?? opts.fail?.[`${init?.method} ${path}`]
    if (forced) return new Response('{"message":"boom secret-detail"}', { status: forced })
    if (init?.method === 'GET' && path.startsWith('contents/')) {
      return new Response('{}', { status: files.has(path.slice('contents/'.length)) ? 200 : 404 })
    }
    if (init?.method === 'PUT' && path.startsWith('contents/')) {
      const p = path.slice('contents/'.length)
      if (files.has(p)) return new Response('{}', { status: 422 })
      files.add(p)
      return new Response('{}', { status: 201 })
    }
    if (init?.method === 'POST' && path === 'issues') {
      issues.push(body)
      return new Response(JSON.stringify({ number: issues.length }), { status: 201 })
    }
    return new Response('{}', { status: 500 })
  }) as typeof fetch
  return { f, files, issues, calls }
}

function setup(gh = fakeGitHub(), env: Partial<Env> = {}) {
  const kv = new MemoryKV()
  const full: Env = { REPORTS_GITHUB_TOKEN: 'ghp_secret_token_canary', REPORTS_REPO: 'acme/kubron-reports', RATE: kv, ...env }
  const call = (body: unknown, init: { headers?: Record<string, string> } = {}) =>
    handleReport(
      new Request('https://kubron.pages.dev/api/bugreport', {
        method: 'POST',
        headers: { 'content-type': 'application/json', 'cf-connecting-ip': '203.0.113.7', ...(init.headers ?? {}) },
        body: typeof body === 'string' ? body : JSON.stringify(body),
      }),
      full,
      { fetch: gh.f, now: () => NOW },
    )
  return { gh, kv, env: full, call }
}

const report = (over: Record<string, unknown> = {}) => ({
  id: ID, description: 'the grid is empty', contact: 'me@example.org', screenshot: pngB64(),
  diagnostics: { appVersion: '1.2.3', shell: 'desktop', os: 'linux', arch: 'amd64', locale: 'ru', view: 'pods' },
  ...over,
})

test('stores the screenshot in the private repository and opens an issue', async () => {
  const s = setup()
  const res = await s.call(report())
  assert.equal(res.status, 201)
  const reply = await res.text()
  assert.deepEqual(JSON.parse(reply), { status: 'created' })
  assert.ok(!reply.includes('ghp_'), 'the token must never be in a reply')

  // Порядок: проверка повтора, снимок, issue, отметка.
  assert.deepEqual(s.gh.calls.map((c) => `${c.method} ${c.path.split('?')[0]}`), [
    `GET contents/${markerPath(ID)}`,
    `PUT contents/${screenshotPath(ID)}`,
    'POST issues',
    `PUT contents/${markerPath(ID)}`,
  ])
  assert.ok(s.gh.calls.every((c) => c.auth === 'Bearer ghp_secret_token_canary'))
  const put = s.gh.calls[1].body
  assert.equal(put.branch, 'main')
  assert.equal(put.content, pngB64())

  const issue = s.gh.issues[0]
  assert.equal(issue.title, '[bug] the grid is empty')
  assert.deepEqual(issue.labels, ['bug-report'])
  assert.match(issue.body, /!\[screenshot\]\(https:\/\/github\.com\/acme\/kubron-reports\/blob\/main\/reports\/0123[0-9a-f]+\/screenshot\.png\?raw=true\)/)
  assert.match(issue.body, /Kubron 1\.2\.3 \(desktop\)/)
  assert.match(issue.body, /`me@example\.org`/)
  assert.ok(s.gh.files.has(markerPath(ID)))
})

test('a report without a screenshot makes no file but the marker', async () => {
  const s = setup()
  const res = await s.call(report({ screenshot: '', contact: '' }))
  assert.equal(res.status, 201)
  assert.deepEqual(s.gh.calls.map((c) => c.method), ['GET', 'POST', 'PUT'])
  assert.match(s.gh.issues[0].body, /Screenshot: not attached/)
  assert.match(s.gh.issues[0].body, /Contact \| not given/)
})

test('a repeated report does not create a second issue', async () => {
  const s = setup()
  assert.equal((await s.call(report())).status, 201)
  const again = await s.call(report())
  assert.equal(again.status, 200)
  assert.deepEqual(await again.json(), { status: 'duplicate' })
  assert.equal(s.gh.issues.length, 1)
})

test('a retry after a partial failure completes the report', async () => {
  // Снимок лёг, issue создать не удалось (502 клиенту), при повторе снимок уже есть (422), issue создаётся.
  const files = new Set<string>()
  const failing = setup(fakeGitHub({ files, fail: { 'POST issues': 500 } }))
  assert.equal((await failing.call(report())).status, 502)
  assert.ok(files.has(screenshotPath(ID)))
  assert.ok(!files.has(markerPath(ID)))

  const ok = setup(fakeGitHub({ files }))
  assert.equal((await ok.call(report())).status, 201)
  assert.equal(ok.gh.issues.length, 1)
})

test('the issue is kept even when the marker cannot be saved', async () => {
  const s = setup(fakeGitHub({ fail: { [`PUT contents/${markerPath(ID)}`]: 500 } }))
  const res = await s.call(report())
  assert.equal(res.status, 201, 'the client must not repeat a report that is already an issue')
  assert.equal(s.gh.issues.length, 1)
})

test('GitHub failures become 502 without leaking details', async () => {
  for (const [key, status] of [['GET contents', 500], ['PUT contents', 403], ['POST issues', 401], ['POST issues', 403]] as const) {
    const s = setup(fakeGitHub({ fail: { [key]: status } }))
    const res = await s.call(report())
    const text = await res.text()
    assert.equal(res.status, 502, `${key} ${status}`)
    assert.ok(!text.includes('secret-detail') && !text.includes('ghp_'), text)
  }
})

test('a missing file is told apart from a failed lookup', async () => {
  // 500 при проверке повтора не означает «файла нет»: иначе при сбое GitHub появлялись бы дубли.
  const s = setup(fakeGitHub({ fail: { 'GET contents': 500 } }))
  assert.equal((await s.call(report())).status, 502)
  assert.equal(s.gh.issues.length, 0)
})

test('is closed until it is configured', async () => {
  for (const env of [{ REPORTS_GITHUB_TOKEN: '' }, { REPORTS_REPO: '' }, { REPORTS_REPO: 'no-slash' }, { REPORTS_REPO: 'a/b/../../c' }, { RATE: undefined }, { REPORTS_BRANCH: 'bad branch' }]) {
    const s = setup(fakeGitHub(), env)
    assert.equal((await s.call(report())).status, 503, JSON.stringify(Object.keys(env)))
    assert.equal(s.gh.calls.length, 0)
  }
})

test('answers only to POST with JSON', async () => {
  const s = setup()
  const get = await handleReport(new Request('https://x/api/bugreport'), s.env, { fetch: s.gh.f, now: () => NOW })
  assert.equal(get.status, 405)
  assert.equal(get.headers.get('allow'), 'POST')
  const text = await s.call(report(), { headers: { 'content-type': 'text/plain' } })
  assert.equal(text.status, 415)
  assert.equal(s.gh.calls.length, 0)
})

test('rejects what is not a valid report', async () => {
  const cases: Array<[string, unknown, number]> = [
    ['not json', '{oops', 400],
    ['array', [], 400],
    ['null', 'null', 400],
    ['no id', report({ id: '' }), 400],
    ['upper-case id', report({ id: ID.toUpperCase() }), 400],
    ['id with a path', report({ id: '../../etc/passwd' + '0'.repeat(16) }), 400],
    ['empty text', report({ description: '   ' }), 400],
    ['text is not a string', report({ description: 5 }), 400],
    ['too long', report({ description: 'я'.repeat(MAX_DESCRIPTION + 1) }), 400],
    ['bad contact', report({ contact: 'not an address' }), 400],
    ['contact with markup', report({ contact: 'a@b.c<img>' }), 400],
    ['bad base64', report({ screenshot: '***!' }), 400],
    ['base64 of text', report({ screenshot: Buffer.from('x'.repeat(100)).toString('base64') }), 400],
    ['tiny', report({ screenshot: 'AAAA' }), 400],
    ['zero width', report({ screenshot: pngB64(0, 4) }), 400],
    ['huge side', report({ screenshot: pngB64(16385, 4) }), 400],
    ['too large', report({ screenshot: pngB64(4, 4, MAX_SCREENSHOT_BYTES) }), 413],
  ]
  for (const [name, body, status] of cases) {
    const s = setup()
    const res = await s.call(body)
    assert.equal(res.status, status, name)
    assert.equal(s.gh.calls.length, 0, `${name}: nothing may reach GitHub`)
  }
})

test('the size of the body is limited even when Content-Length is absent or false', async () => {
  const big = new Uint8Array(1024)
  const stream = (n: number) => new ReadableStream<Uint8Array>({
    pull(c) { n--; n >= 0 ? c.enqueue(big) : c.close() },
  })
  const req = new Request('https://x/', { method: 'POST', body: stream(20), duplex: 'half' } as RequestInit)
  await assert.rejects(() => readLimited(req, 4096), (e: any) => e.status === 413)
  const liar = new Request('https://x/', { method: 'POST', body: 'x', headers: { 'content-length': '999999999' } })
  await assert.rejects(() => readLimited(liar, 4096), (e: any) => e.status === 413)
  const ok = new Request('https://x/', { method: 'POST', body: 'abc' })
  assert.equal(new TextDecoder().decode(await readLimited(ok, 4096)), 'abc')
})

test('limits reports per address and per day', async () => {
  const s = setup()
  for (let i = 0; i < LIMIT_PER_IP_HOUR; i++) assert.equal((await s.call(report({ id: i.toString(16).padStart(32, '0') }))).status, 201)
  const over = await s.call(report({ id: 'f'.repeat(32) }))
  assert.equal(over.status, 429)
  assert.equal(over.headers.get('retry-after'), '3600')
  // Другой адрес не затронут.
  const other = await s.call(report({ id: 'e'.repeat(32) }), { headers: { 'cf-connecting-ip': '198.51.100.9' } })
  assert.equal(other.status, 201)

  const day = setup()
  day.kv.data.set('all:2026-11-10', String(LIMIT_PER_DAY))
  assert.equal((await day.call(report())).status, 429)
  assert.equal(day.gh.calls.length, 0, 'over the limit nothing is stored')
})

test('a refused report does not use up the quota of valid ones', async () => {
  const s = setup()
  for (let i = 0; i < 3 * LIMIT_PER_IP_HOUR; i++) await s.call(report({ description: '' }))
  assert.equal((await s.call(report())).status, 201)
})

test('the text cannot escape its block, mention anyone or reference issues', async () => {
  const nasty = 'start\n```\n# heading\n@octocat please look at #1 and <img src=x onerror=alert(1)>\n`````\n[link](javascript:alert(1))'
  const s = setup()
  assert.equal((await s.call(report({ description: nasty }))).status, 201)
  const body: string = s.gh.issues[0].body
  const f = fence(nasty)
  assert.equal(f, '``````') // longer than the longest run (5)
  assert.ok(body.startsWith(f + '\n' + nasty + '\n' + f + '\n'), 'the whole text is inside one block')
  assert.ok(!body.slice(f.length + nasty.length + 2 * 1 + f.length).includes('@octocat'), 'nothing of it after the block')
})

test('fields are cleaned and bounded', () => {
  const r = parseReport(report({
    description: `a\u0000b${RLO}c\r\nd`,
    diagnostics: { appVersion: '1.0\n[x](y)', shell: 'x'.repeat(500), os: '\u0007linux', arch: 5, locale: null, view: ' pods ' },
  }))
  assert.equal(r.description, 'abc\nd')
  assert.equal(r.diagnostics.appVersion, '1.0 [x](y)')
  assert.equal(r.diagnostics.shell.length, 64)
  assert.equal(r.diagnostics.os, 'linux')
  assert.equal(r.diagnostics.arch, '')
  assert.equal(r.diagnostics.view, 'pods')
  assert.equal(oneLine('  a \n\t b  ', 10), 'a b')
})

test('the title is one short line', () => {
  assert.equal(issueTitle('first line\nsecond'), '[bug] first line')
  assert.equal(issueTitle('\n\nbody only'), '[bug] (no title)')
  assert.equal(issueTitle('see @octocat and octocat/hello#12 or #3'), '[bug] see octocat and octocat/hello12 or 3')
  assert.equal(Array.from(issueTitle('я'.repeat(300))).length, '[bug] '.length + 80)
  assert.ok(!issueTitle(`a\u0000${RLO}b`).includes(RLO))
})

test('a lone surrogate does not break the report', () => {
  const r = parseReport(report({ description: 'ok \ud800 end' }))
  assert.equal(r.description, 'ok � end')
})
