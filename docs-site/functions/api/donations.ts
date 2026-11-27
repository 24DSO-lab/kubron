// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

interface Env {
  DONATIONS_WORKER_URL?: string
}

interface Context {
  request: Request
  env: Env
}

export const onRequestGet = async ({ request, env }: Context): Promise<Response> => {
  const base = env.DONATIONS_WORKER_URL?.trim()
  if (!base) {
    return Response.json({ error: 'donations_service_not_configured' }, { status: 503 })
  }

  try {
    const target = new URL('/api/donations', base)
    const upstream = await fetch(target, {
      headers: { accept: 'application/json' },
      signal: AbortSignal.timeout(10000),
    })
    const headers = new Headers(upstream.headers)
    headers.set('cache-control', 'public, max-age=60, stale-while-revalidate=300')
    return new Response(upstream.body, { status: upstream.status, headers })
  } catch {
    return Response.json({ error: 'donations_service_unavailable' }, { status: 502 })
  }
}
