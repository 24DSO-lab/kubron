// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { DonationAlertsEnv, KVNamespaceLike } from './donationalerts.ts'

interface SyncEnv extends DonationAlertsEnv {
  DONATIONALERTS_SYNC_SECRET?: string
}

const TOKEN_URL = 'https://www.donationalerts.com/oauth/token'
const DONATIONS_URL = 'https://www.donationalerts.com/api/v1/alerts/donations'
const TOKEN_KEY = 'oauth:donationalerts:tokens'
const SCOPE = 'oauth-donation-index'
const MAX_PAGES = 20
const PAGE_SIZE = 30

function json(status: number, body: Record<string, unknown>): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' },
  })
}

function fromBase64(value: string): Uint8Array {
  const binary = atob(value)
  return Uint8Array.from(binary, char => char.charCodeAt(0))
}

function toBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

async function keyFromSecret(secret: string): Promise<CryptoKey> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(secret))
  return crypto.subtle.importKey('raw', digest, { name: 'AES-GCM' }, false, ['decrypt', 'encrypt'])
}

async function readTokens(env: SyncEnv): Promise<Record<string, unknown> | null> {
  const raw = await env.DONATIONALERTS.get(TOKEN_KEY)
  if (!raw) return null
  const envelope = JSON.parse(raw) as { version?: number; iv?: string; data?: string }
  if (envelope.version !== 1 || !envelope.iv || !envelope.data) throw new Error('Stored token envelope is invalid')
  const key = await keyFromSecret(env.DONATIONALERTS_TOKEN_ENCRYPTION_KEY ?? '')
  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: fromBase64(envelope.iv) },
    key,
    fromBase64(envelope.data),
  )
  return JSON.parse(new TextDecoder().decode(plain)) as Record<string, unknown>
}

async function writeTokens(env: SyncEnv, tokens: Record<string, unknown>): Promise<void> {
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const key = await keyFromSecret(env.DONATIONALERTS_TOKEN_ENCRYPTION_KEY ?? '')
  const plain = new TextEncoder().encode(JSON.stringify(tokens))
  const encrypted = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, plain)
  await env.DONATIONALERTS.put(TOKEN_KEY, JSON.stringify({
    version: 1,
    iv: toBase64(iv),
    data: toBase64(new Uint8Array(encrypted)),
  }))
}

async function constantTimeEqual(left: string, right: string): Promise<boolean> {
  const [a, b] = await Promise.all([
    crypto.subtle.digest('SHA-256', new TextEncoder().encode(left)),
    crypto.subtle.digest('SHA-256', new TextEncoder().encode(right)),
  ])
  const x = new Uint8Array(a), y = new Uint8Array(b)
  let mismatch = 0
  for (let i = 0; i < x.length; i++) mismatch |= x[i] ^ y[i]
  return mismatch === 0 && left.length > 0
}

async function fetchJson(url: string, init?: RequestInit): Promise<Record<string, any>> {
  const response = await fetch(url, { ...init, signal: AbortSignal.timeout(15000) })
  if (!response.ok) {
    await response.body?.cancel()
    throw new Error(`DonationAlerts API returned HTTP ${response.status}`)
  }
  return await response.json() as Record<string, any>
}

async function validAccessToken(env: SyncEnv, tokens: Record<string, unknown>): Promise<Record<string, unknown>> {
  const expiresIn = Number(tokens.expires_in)
  const obtainedAt = Date.parse(String(tokens.obtained_at ?? ''))
  const stillValid = typeof tokens.access_token === 'string' && Number.isFinite(expiresIn) &&
    Number.isFinite(obtainedAt) && obtainedAt + expiresIn * 1000 > Date.now() + 60_000
  if (stillValid) return tokens
  if (typeof tokens.refresh_token !== 'string' || !tokens.refresh_token) throw new Error('DonationAlerts refresh token is missing')

  const body = new URLSearchParams({
    grant_type: 'refresh_token',
    refresh_token: tokens.refresh_token,
    client_id: env.DONATIONALERTS_CLIENT_ID ?? '',
    client_secret: env.DONATIONALERTS_CLIENT_SECRET ?? '',
    scope: SCOPE,
  })
  const refreshed = await fetchJson(TOKEN_URL, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json' },
    body: body.toString(),
  })
  if (typeof refreshed.access_token !== 'string' || typeof refreshed.expires_in !== 'number') {
    throw new Error('DonationAlerts returned an invalid refreshed token')
  }
  const next = {
    ...tokens,
    access_token: refreshed.access_token,
    refresh_token: typeof refreshed.refresh_token === 'string' ? refreshed.refresh_token : tokens.refresh_token,
    expires_in: refreshed.expires_in,
    obtained_at: new Date().toISOString(),
  }
  await writeTokens(env, next)
  return next
}

function isRecentDonation(value: unknown): boolean {
  if (typeof value !== 'string') return false
  const timestamp = Date.parse(value.includes('T') ? value : value.replace(' ', 'T') + 'Z')
  return Number.isFinite(timestamp) && timestamp >= Date.now() - 30 * 24 * 60 * 60 * 1000
}

async function getRecentDonations(accessToken: string): Promise<Record<string, unknown>[]> {
  const donations: Record<string, unknown>[] = []
  let next: string | null = DONATIONS_URL
  let pages = 0
  while (next && pages < MAX_PAGES) {
    const pageUrl = new URL(next)
    if (pageUrl.origin !== 'https://www.donationalerts.com' || pageUrl.pathname !== '/api/v1/alerts/donations') {
      throw new Error('DonationAlerts returned an unexpected pagination URL')
    }
    const page = await fetchJson(pageUrl.toString(), {
      headers: { accept: 'application/json', authorization: `Bearer ${accessToken}` },
    })
    if (!Array.isArray(page.data)) throw new Error('DonationAlerts returned an invalid donations page')
    const items = page.data as Record<string, unknown>[]
    for (const item of items) {
      if (isRecentDonation(item.created_at)) {
        donations.push({
          id: item.id,
          username: item.username,
          amount: item.amount,
          currency: item.currency,
          created_at: item.created_at,
        })
      }
    }
    pages++
    const oldest = items.map(item => typeof item.created_at === 'string' ? Date.parse(item.created_at.replace(' ', 'T') + 'Z') : NaN)
      .filter(Number.isFinite).reduce((min, value) => Math.min(min, value), Number.POSITIVE_INFINITY)
    if (oldest < Date.now() - 30 * 24 * 60 * 60 * 1000) break
    const links = page.links as { next?: unknown } | undefined
    next = typeof links?.next === 'string' ? links.next : null
    if (next && pages < MAX_PAGES) await new Promise(resolve => setTimeout(resolve, 1000))
  }
  if (next && pages >= MAX_PAGES) throw new Error('DonationAlerts history exceeded the safe pagination limit')
  return donations
}

export async function handleDonationAlertsSync(request: Request, env: SyncEnv): Promise<Response> {
  if (request.method !== 'POST') return json(405, { error: 'method_not_allowed' })
  const configuredSecret = env.DONATIONALERTS_SYNC_SECRET ?? ''
  const suppliedSecret = request.headers.get('authorization')?.replace(/^Bearer\s+/i, '') ?? ''
  if (configuredSecret.length < 32 || !(await constantTimeEqual(configuredSecret, suppliedSecret))) {
    return json(401, { error: 'unauthorized' })
  }
  if (!env.DONATIONALERTS || !env.DONATIONALERTS_CLIENT_ID || !env.DONATIONALERTS_CLIENT_SECRET ||
      (env.DONATIONALERTS_TOKEN_ENCRYPTION_KEY ?? '').length < 32) {
    return json(503, { error: 'integration_not_configured' })
  }
  try {
    const stored = await readTokens(env)
    if (!stored) return json(409, { error: 'donationalerts_not_connected' })
    const tokens = await validAccessToken(env, stored)
    if (typeof tokens.access_token !== 'string') throw new Error('DonationAlerts access token is missing')
    const donations = await getRecentDonations(tokens.access_token)
    return json(200, { data: donations })
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Unknown synchronization error'
    return json(502, { error: 'donationalerts_sync_failed', message })
  }
}
