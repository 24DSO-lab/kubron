// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Server-side OAuth 2.0 integration for DonationAlerts. Tokens are encrypted before being stored in KV. */

export interface KVNamespaceLike {
  get(key: string): Promise<string | null>
  put(key: string, value: string, options?: { expirationTtl?: number }): Promise<void>
  delete(key: string): Promise<void>
}

export interface DonationAlertsEnv {
  DONATIONALERTS: KVNamespaceLike
  DONATIONALERTS_CLIENT_ID?: string
  DONATIONALERTS_CLIENT_SECRET?: string
  DONATIONALERTS_TOKEN_ENCRYPTION_KEY?: string
  DONATIONALERTS_REDIRECT_URI?: string
}

export interface Runtime {
  fetch: typeof fetch
  crypto: Crypto
}

const AUTH_URL = 'https://www.donationalerts.com/oauth/authorize'
const TOKEN_URL = 'https://www.donationalerts.com/oauth/token'
const SCOPES = ['oauth-donation-index']
const STATE_TTL_SECONDS = 600
const TOKEN_KEY = 'oauth:donationalerts:tokens'
const STATE_PREFIX = 'oauth:donationalerts:state:'

function json(status: number, body: Record<string, string>): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' },
  })
}

function html(status: number, title: string, message: string): Response {
  const escape = (s: string) => s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
  return new Response(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>${escape(title)}</title><body style="font:16px system-ui;max-width:38rem;margin:12vh auto;padding:1.5rem"><h1>${escape(title)}</h1><p>${escape(message)}</p><p>You can close this window.</p></body></html>`, {
    status,
    headers: { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff', 'content-security-policy': "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'" },
  })
}

function config(env: DonationAlertsEnv): { clientId: string; clientSecret: string; encryptionKey: string } | null {
  const clientId = env.DONATIONALERTS_CLIENT_ID?.trim() ?? ''
  const clientSecret = env.DONATIONALERTS_CLIENT_SECRET ?? ''
  const encryptionKey = env.DONATIONALERTS_TOKEN_ENCRYPTION_KEY ?? ''
  if (!env.DONATIONALERTS || !/^\d+$/.test(clientId) || clientSecret.length < 1 || encryptionKey.length < 32) return null
  return { clientId, clientSecret, encryptionKey }
}

function redirectUri(env: DonationAlertsEnv): string {
  const configured = env.DONATIONALERTS_REDIRECT_URI?.trim()
  if (!configured) throw new Error('Redirect URI must be configured explicitly')
  const uri = configured
  const parsed = new URL(uri)
  if (parsed.protocol !== 'https:' || parsed.pathname !== '/api/donationalerts/callback' || parsed.search || parsed.hash) {
    throw new Error('Invalid redirect URI configuration')
  }
  return parsed.toString()
}

function toBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

async function encryptionKey(secret: string, cryptoImpl: Crypto): Promise<CryptoKey> {
  const digest = await cryptoImpl.subtle.digest('SHA-256', new TextEncoder().encode(secret))
  return cryptoImpl.subtle.importKey('raw', digest, { name: 'AES-GCM' }, false, ['encrypt'])
}

async function encryptTokens(tokens: unknown, secret: string, cryptoImpl: Crypto): Promise<string> {
  const iv = cryptoImpl.getRandomValues(new Uint8Array(12))
  const key = await encryptionKey(secret, cryptoImpl)
  const plain = new TextEncoder().encode(JSON.stringify(tokens))
  const cipher = await cryptoImpl.subtle.encrypt({ name: 'AES-GCM', iv }, key, plain)
  return JSON.stringify({ version: 1, iv: toBase64(iv), data: toBase64(new Uint8Array(cipher)) })
}

export async function handleConnect(request: Request, env: DonationAlertsEnv, runtime: Runtime = { fetch: globalThis.fetch.bind(globalThis), crypto: globalThis.crypto }): Promise<Response> {
  if (request.method !== 'GET') return json(405, { error: 'method_not_allowed' })
  const cfg = config(env)
  if (!cfg) return json(503, { error: 'integration_not_configured' })
  let callback: string
  try { callback = redirectUri(env) } catch { return json(503, { error: 'integration_not_configured' }) }

  const stateBytes = runtime.crypto.getRandomValues(new Uint8Array(32))
  const state = toBase64(stateBytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
  await env.DONATIONALERTS.put(STATE_PREFIX + state, 'pending', { expirationTtl: STATE_TTL_SECONDS })

  const authorize = new URL(AUTH_URL)
  authorize.searchParams.set('client_id', cfg.clientId)
  authorize.searchParams.set('redirect_uri', callback)
  authorize.searchParams.set('response_type', 'code')
  authorize.searchParams.set('scope', SCOPES.join(' '))
  authorize.searchParams.set('state', state)
  return Response.redirect(authorize.toString(), 302)
}

export async function handleCallback(request: Request, env: DonationAlertsEnv, runtime: Runtime = { fetch: globalThis.fetch.bind(globalThis), crypto: globalThis.crypto }): Promise<Response> {
  if (request.method !== 'GET') return json(405, { error: 'method_not_allowed' })
  const cfg = config(env)
  if (!cfg) return html(503, 'Kubron · DonationAlerts', 'The integration is not configured yet.')

  const url = new URL(request.url)
  if (url.searchParams.has('error')) return html(400, 'Kubron · DonationAlerts', 'Authorization was declined or failed. You can close this window.')
  const code = url.searchParams.get('code') ?? ''
  const state = url.searchParams.get('state') ?? ''
  if (!code || code.length > 2048 || !/^[A-Za-z0-9_-]{32,128}$/.test(state)) {
    return html(400, 'Kubron · DonationAlerts', 'The authorization response is invalid. Please start again.')
  }

  const stateKey = STATE_PREFIX + state
  const pending = await env.DONATIONALERTS.get(stateKey)
  if (pending !== 'pending') return html(400, 'Kubron · DonationAlerts', 'The authorization session expired or was already used. Please start again.')
  await env.DONATIONALERTS.delete(stateKey)

  let callback: string
  try { callback = redirectUri(env) } catch { return html(503, 'Kubron · DonationAlerts', 'The integration is not configured yet.') }

  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    client_id: cfg.clientId,
    client_secret: cfg.clientSecret,
    redirect_uri: callback,
    code,
  })
  let response: Response
  try {
    response = await runtime.fetch(TOKEN_URL, {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json' },
      body: body.toString(),
      signal: AbortSignal.timeout(15000),
    })
  } catch {
    return html(502, 'Kubron · DonationAlerts', 'DonationAlerts could not be reached. Please start authorization again.')
  }
  if (!response.ok) {
    await response.body?.cancel()
    return html(502, 'Kubron · DonationAlerts', 'DonationAlerts rejected the authorization exchange. Check the application settings and try again.')
  }

  let tokens: Record<string, unknown>
  try { tokens = await response.json() as Record<string, unknown> } catch {
    return html(502, 'Kubron · DonationAlerts', 'DonationAlerts returned an invalid authorization response.')
  }
  if (typeof tokens.access_token !== 'string' || typeof tokens.refresh_token !== 'string' ||
      typeof tokens.expires_in !== 'number' || !Number.isFinite(tokens.expires_in)) {
    return html(502, 'Kubron · DonationAlerts', 'DonationAlerts did not return the required tokens.')
  }

  const stored = await encryptTokens({
    access_token: tokens.access_token,
    refresh_token: tokens.refresh_token,
    expires_in: tokens.expires_in,
    obtained_at: new Date().toISOString(),
    scope: SCOPES,
  }, cfg.encryptionKey, runtime.crypto)
  await env.DONATIONALERTS.put(TOKEN_KEY, stored)
  return html(200, 'Kubron · DonationAlerts', 'DonationAlerts has been connected successfully. You can close this window.')
}

/** Public handler for a status check. Never returns tokens or account details. */
export async function handleStatus(request: Request, env: DonationAlertsEnv): Promise<Response> {
  if (request.method !== 'GET') return json(405, { error: 'method_not_allowed' })
  if (!env.DONATIONALERTS) return json(503, { error: 'integration_not_configured' })
  const connected = (await env.DONATIONALERTS.get(TOKEN_KEY)) !== null
  return json(200, { connected: String(connected) })
}
