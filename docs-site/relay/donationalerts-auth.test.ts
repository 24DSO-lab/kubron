// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { handleOwnerConnect, handleOwnerSession, hasOwnerSession, type OwnerAuthEnv } from './donationalerts-auth.ts'
import type { KVNamespaceLike } from './donationalerts.ts'

class MemoryKV implements KVNamespaceLike {
  values = new Map<string, string>()
  async get(key: string) { return this.values.get(key) ?? null }
  async put(key: string, value: string) { this.values.set(key, value) }
  async delete(key: string) { this.values.delete(key) }
}
const secret = 'owner-only-test-secret-that-is-at-least-32-chars'
const makeEnv = (): OwnerAuthEnv & { DONATIONALERTS: MemoryKV } => ({ DONATIONALERTS: new MemoryKV(), DONATIONALERTS_CONNECT_SECRET: secret })

test('connect is gated until a valid signed owner session exists', async () => {
  const env = makeEnv()
  const request = new Request('https://kubron.pages.dev/api/donationalerts/connect')
  const denied = await handleOwnerConnect(request, env, async () => new Response('oauth'))
  assert.equal(denied.status, 200)
  assert.match(await denied.text(), /Admin sign-in/)
  const login = await handleOwnerSession(new Request('https://kubron.pages.dev/api/donationalerts/session', {
    method: 'POST', headers: { origin: 'https://kubron.pages.dev', 'content-type': 'application/json' },
    body: JSON.stringify({ secret }),
  }), env)
  assert.equal(login.status, 200)
  const cookie = login.headers.get('set-cookie')!
  assert.match(cookie, /HttpOnly/)
  assert.match(cookie, /Secure/)
  assert.match(cookie, /SameSite=Strict/)
  const allowed = await handleOwnerConnect(new Request(request, { headers: { cookie: cookie.split(';')[0] } }), env, async () => new Response('oauth'))
  assert.equal(await allowed.text(), 'oauth')
})

test('session endpoint rejects cross-origin and incorrect credentials', async () => {
  const env = makeEnv()
  const crossOrigin = await handleOwnerSession(new Request('https://kubron.pages.dev/api/donationalerts/session', {
    method: 'POST', headers: { origin: 'https://attacker.example', 'content-type': 'application/json' },
    body: JSON.stringify({ secret }),
  }), env)
  assert.equal(crossOrigin.status, 403)
  const wrong = await handleOwnerSession(new Request('https://kubron.pages.dev/api/donationalerts/session', {
    method: 'POST', headers: { origin: 'https://kubron.pages.dev', 'content-type': 'application/json', 'cf-connecting-ip': '192.0.2.10' },
    body: JSON.stringify({ secret: 'wrong-secret-value-that-is-long-enough' }),
  }), env)
  assert.equal(wrong.status, 401)
})

test('owner sessions reject tampered and expired cookies', async () => {
  const env = makeEnv()
  const login = await handleOwnerSession(new Request('https://kubron.pages.dev/api/donationalerts/session', {
    method: 'POST', headers: { origin: 'https://kubron.pages.dev', 'content-type': 'application/json' },
    body: JSON.stringify({ secret }),
  }), env)
  const cookie = login.headers.get('set-cookie')!.split(';')[0]
  const request = new Request('https://kubron.pages.dev/api/donationalerts/connect', { headers: { cookie } })
  assert.equal(await hasOwnerSession(request, secret), true)
  assert.equal(await hasOwnerSession(new Request(request, { headers: { cookie: cookie + 'tampered' } }), secret), false)
})
