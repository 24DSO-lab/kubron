// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { webcrypto } from 'node:crypto'
import { handleCallback, handleConnect, handleStatus, type DonationAlertsEnv, type KVNamespaceLike } from './donationalerts.ts'

class MemoryKV implements KVNamespaceLike {
  values = new Map<string, string>()
  async get(key: string) { return this.values.get(key) ?? null }
  async put(key: string, value: string) { this.values.set(key, value) }
  async delete(key: string) { this.values.delete(key) }
}

const cryptoImpl = webcrypto as unknown as Crypto
const env = (): DonationAlertsEnv & { DONATIONALERTS: MemoryKV } => ({
  DONATIONALERTS: new MemoryKV(),
  DONATIONALERTS_CLIENT_ID: '123456',
  DONATIONALERTS_CLIENT_SECRET: 'client-secret-canary',
  DONATIONALERTS_TOKEN_ENCRYPTION_KEY: 'a-long-test-encryption-secret-value',
})

test('connect redirects to DonationAlerts with minimal scope and a one-time state', async () => {
  const e = env()
  const response = await handleConnect(new Request('https://kubron.pages.dev/api/donationalerts/connect'), e, { fetch, crypto: cryptoImpl })
  assert.equal(response.status, 302)
  const location = new URL(response.headers.get('location')!)
  assert.equal(location.origin, 'https://www.donationalerts.com')
  assert.equal(location.pathname, '/oauth/authorize')
  assert.equal(location.searchParams.get('client_id'), '123456')
  assert.equal(location.searchParams.get('redirect_uri'), 'https://kubron.pages.dev/api/donationalerts/callback')
  assert.equal(location.searchParams.get('response_type'), 'code')
  assert.equal(location.searchParams.get('scope'), 'oauth-donation-index')
  const state = location.searchParams.get('state')!
  assert.match(state, /^[A-Za-z0-9_-]{43}$/)
  assert.equal(await e.DONATIONALERTS.get('oauth:donationalerts:state:' + state), 'pending')
})

test('callback exchanges code, stores encrypted tokens, and consumes state', async () => {
  const e = env()
  const state = 'a'.repeat(43)
  await e.DONATIONALERTS.put('oauth:donationalerts:state:' + state, 'pending')
  let requestBody = ''
  const fetcher = (async (input: string | URL | Request, init?: RequestInit) => {
    assert.equal(String(input), 'https://www.donationalerts.com/oauth/token')
    requestBody = String(init?.body)
    return Response.json({ access_token: 'access-token-canary', refresh_token: 'refresh-token-canary', expires_in: 3600 })
  }) as typeof fetch
  const response = await handleCallback(new Request('https://kubron.pages.dev/api/donationalerts/callback?code=one-time-code&state=' + state), e, { fetch: fetcher, crypto: cryptoImpl })
  assert.equal(response.status, 200)
  assert.match(await response.text(), /connected successfully/)
  assert.match(requestBody, /grant_type=authorization_code/)
  assert.match(requestBody, /client_secret=client-secret-canary/)
  assert.equal(await e.DONATIONALERTS.get('oauth:donationalerts:state:' + state), null)
  const stored = await e.DONATIONALERTS.get('oauth:donationalerts:tokens')
  assert.ok(stored)
  assert.ok(!stored.includes('access-token-canary'))
  assert.ok(!stored.includes('refresh-token-canary'))
  assert.ok(!stored.includes('client-secret-canary'))
  const parsed = JSON.parse(stored!)
  assert.equal(parsed.version, 1)
})

test('callback rejects missing, expired, and reused state', async () => {
  const e = env()
  const response = await handleCallback(new Request('https://kubron.pages.dev/api/donationalerts/callback?code=x&state=' + 'b'.repeat(43)), e, { fetch, crypto: cryptoImpl })
  assert.equal(response.status, 400)
  assert.match(await response.text(), /expired or was already used/)
})

test('callback does not store tokens when the token exchange fails', async () => {
  const e = env()
  const state = 'c'.repeat(43)
  await e.DONATIONALERTS.put('oauth:donationalerts:state:' + state, 'pending')
  const fetcher = (async () => new Response('secret upstream body', { status: 401 })) as typeof fetch
  const response = await handleCallback(new Request('https://kubron.pages.dev/api/donationalerts/callback?code=x&state=' + state), e, { fetch: fetcher, crypto: cryptoImpl })
  assert.equal(response.status, 502)
  assert.equal(await e.DONATIONALERTS.get('oauth:donationalerts:tokens'), null)
  assert.ok(!(await response.text()).includes('secret upstream body'))
})

test('status exposes only a boolean and no token material', async () => {
  const e = env()
  const response = await handleStatus(new Request('https://kubron.pages.dev/api/donationalerts/status'), e)
  assert.deepEqual(await response.json(), { connected: 'false' })
  await e.DONATIONALERTS.put('oauth:donationalerts:tokens', '{"encrypted":"value"}')
  const connected = await handleStatus(new Request('https://kubron.pages.dev/api/donationalerts/status'), e)
  assert.deepEqual(await connected.json(), { connected: 'true' })
})

test('configuration errors fail closed', async () => {
  const e = env()
  e.DONATIONALERTS_CLIENT_SECRET = ''
  const response = await handleConnect(new Request('https://kubron.pages.dev/api/donationalerts/connect'), e, { fetch, crypto: cryptoImpl })
  assert.equal(response.status, 503)
  assert.equal(e.DONATIONALERTS.values.size, 0)
})
