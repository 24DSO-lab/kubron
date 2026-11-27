// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { KVNamespaceLike } from './donationalerts.ts'

export interface OwnerAuthEnv {
  DONATIONALERTS: KVNamespaceLike
  DONATIONALERTS_CONNECT_SECRET?: string
}

const COOKIE = '__Host-kubron-da-session'
const SESSION_SECONDS = 30 * 60
const ATTEMPT_SECONDS = 15 * 60
const MAX_ATTEMPTS = 5
const ATTEMPT_PREFIX = 'auth:donationalerts:attempts:'

function b64url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}
function fromB64url(value: string): Uint8Array {
  const binary = atob(value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4))
  return Uint8Array.from(binary, c => c.charCodeAt(0))
}
async function hmac(secret: string, value: string): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign'])
  return new Uint8Array(await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(value)))
}
async function validSignature(secret: string, value: string, signature: string): Promise<boolean> {
  try {
    const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['verify'])
    return await crypto.subtle.verify('HMAC', key, fromB64url(signature), new TextEncoder().encode(value))
  } catch { return false }
}
function cookieValue(request: Request): string {
  for (const part of (request.headers.get('cookie') ?? '').split(';')) {
    const [name, ...rest] = part.trim().split('=')
    if (name === COOKIE) return rest.join('=')
  }
  return ''
}
function page(): Response {
  const body = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Kubron · Admin sign-in</title><body style="font:16px system-ui;max-width:28rem;margin:12vh auto;padding:1.5rem"><h1>Kubron · DonationAlerts</h1><p>Enter the private connection password to continue.</p><form id="login"><label for="secret">Password</label><input id="secret" type="password" autocomplete="current-password" required minlength="32" style="display:block;box-sizing:border-box;width:100%;padding:.7rem;margin:.5rem 0 1rem"><button style="padding:.7rem 1rem">Continue</button><p id="error" role="alert"></p></form><script>document.getElementById('login').addEventListener('submit',async e=>{e.preventDefault();const button=e.target.querySelector('button');button.disabled=true;const error=document.getElementById('error');error.textContent='';try{const r=await fetch('/api/donationalerts/session',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({secret:document.getElementById('secret').value})});if(!r.ok)throw new Error(r.status===429?'Too many attempts. Try again later.':'Invalid password or unavailable service.');location.reload()}catch(err){error.textContent=err.message;button.disabled=false}})</script></body></html>`
  return new Response(body, { status: 200, headers: {
    'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store',
    'x-content-type-options': 'nosniff', 'referrer-policy': 'no-referrer',
    'content-security-policy': "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
  } })
}
function json(status: number, body: Record<string, string>): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' } })
}
function sameOrigin(request: Request): boolean {
  const origin = request.headers.get('origin')
  if (!origin) return false
  try { return new URL(origin).origin === new URL(request.url).origin } catch { return false }
}
async function attemptKey(request: Request, secret: string): Promise<string> {
  const ip = request.headers.get('cf-connecting-ip') ?? 'unknown'
  return ATTEMPT_PREFIX + b64url(await hmac(secret, ip))
}
export async function hasOwnerSession(request: Request, secret: string): Promise<boolean> {
  const raw = cookieValue(request)
  const [payload, signature, extra] = raw.split('.')
  if (!payload || !signature || extra !== undefined || !(await validSignature(secret, payload, signature))) return false
  try {
    const session = JSON.parse(new TextDecoder().decode(fromB64url(payload))) as { exp?: number; nonce?: string }
    return typeof session.exp === 'number' && session.exp > Math.floor(Date.now() / 1000) &&
      typeof session.nonce === 'string' && /^[A-Za-z0-9_-]{20,}$/.test(session.nonce)
  } catch { return false }
}
export async function handleOwnerConnect(request: Request, env: OwnerAuthEnv, connect: () => Promise<Response>): Promise<Response> {
  const secret = env.DONATIONALERTS_CONNECT_SECRET ?? ''
  if (secret.length < 32 || !env.DONATIONALERTS) return json(503, { error: 'owner_auth_not_configured' })
  if (await hasOwnerSession(request, secret)) return connect()
  return page()
}
export async function handleOwnerSession(request: Request, env: OwnerAuthEnv): Promise<Response> {
  if (request.method !== 'POST') return json(405, { error: 'method_not_allowed' })
  if (!sameOrigin(request) || !request.headers.get('content-type')?.toLowerCase().startsWith('application/json')) return json(403, { error: 'invalid_request_origin' })
  const secret = env.DONATIONALERTS_CONNECT_SECRET ?? ''
  if (secret.length < 32 || !env.DONATIONALERTS) return json(503, { error: 'owner_auth_not_configured' })
  const key = await attemptKey(request, secret)
  const count = Number(await env.DONATIONALERTS.get(key) ?? '0')
  if (!Number.isFinite(count) || count >= MAX_ATTEMPTS) return json(429, { error: 'too_many_attempts' })
  let supplied = ''
  try {
    const body = await request.json() as { secret?: unknown }
    if (typeof body.secret === 'string') supplied = body.secret
  } catch { return json(400, { error: 'invalid_json' }) }
  const expectedDigest = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(secret)))
  const suppliedDigest = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(supplied)))
  let difference = 0
  for (let i = 0; i < expectedDigest.length; i++) difference |= expectedDigest[i] ^ suppliedDigest[i]
  if (difference !== 0 || supplied.length < 32) {
    await env.DONATIONALERTS.put(key, String(count + 1), { expirationTtl: ATTEMPT_SECONDS })
    return json(401, { error: 'invalid_credentials' })
  }
  await env.DONATIONALERTS.delete(key)
  const payload = b64url(new TextEncoder().encode(JSON.stringify({
    exp: Math.floor(Date.now() / 1000) + SESSION_SECONDS,
    nonce: b64url(crypto.getRandomValues(new Uint8Array(24))),
  })))
  const signature = b64url(await hmac(secret, payload))
  return new Response(JSON.stringify({ authenticated: true }), { status: 200, headers: {
    'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store',
    'set-cookie': `${COOKIE}=${payload}.${signature}; Path=/; Max-Age=${SESSION_SECONDS}; HttpOnly; Secure; SameSite=Strict`,
  } })
}
