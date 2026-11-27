// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

interface Env {
  DB: D1Database
  DONATIONALERTS_SYNC_SECRET: string
  TRONGRID_API_KEY?: string
}

type Donation = {
  id: string; asset: string; network: string; amount: string; txHash: string
  fromAddress?: string; donorName?: string; receivedAt: string; explorerTxUrl?: string
  source?: string; status: 'confirmed'
}

const BTC_ADDRESS = 'bc1q7qazgg8xqsd7q9ajqvhru0ekmxrj9y6usty3g2'
const EVM_ADDRESS = '0x45c14C29fC24848054bdefC24AB3FfF3de0648F8'
const TRON_ADDRESS = 'TU3ZAL5fdSHnpveC7T89AitgqEs7BdDkph'
const USDC = '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48'
const USDT = 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t'
const WINDOW = 30 * 24 * 60 * 60 * 1000
const MAX_PAGES = 20

const wallets = [
  { id: 'btc-bitcoin', asset: 'BTC', network: 'Bitcoin', address: BTC_ADDRESS, explorerAddressUrl: `https://mempool.space/address/${BTC_ADDRESS}` },
  { id: 'eth-ethereum', asset: 'ETH', network: 'Ethereum', address: EVM_ADDRESS, explorerAddressUrl: `https://etherscan.io/address/${EVM_ADDRESS}` },
  { id: 'usdt-tron', asset: 'USDT', network: 'TRON (TRC-20)', address: TRON_ADDRESS, explorerAddressUrl: `https://tronscan.org/#/address/${TRON_ADDRESS}` },
  { id: 'usdc-ethereum', asset: 'USDC', network: 'Ethereum (ERC-20)', address: EVM_ADDRESS, explorerAddressUrl: `https://etherscan.io/address/${EVM_ADDRESS}` },
]

async function json(url: string, headers: Record<string, string> = {}) {
  const response = await fetch(url, { headers: { accept: 'application/json', ...headers }, signal: AbortSignal.timeout(20000) })
  if (!response.ok) throw new Error(`HTTP ${response.status} from ${new URL(url).host}`)
  return response.json() as Promise<any>
}
function units(value: string | number | bigint, decimals: number): string {
  const n = BigInt(value), base = 10n ** BigInt(decimals), whole = n / base
  const fraction = (n % base).toString().padStart(decimals, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole.toString()
}
function makeTx(asset: string, network: string, hash: string, amount: string, from: string | undefined, time: number, url: string, suffix = ''): Donation {
  return { id: `${network}:${hash.toLowerCase()}${suffix}`, asset, network, amount, txHash: hash,
    ...(from ? { fromAddress: from } : {}), receivedAt: new Date(time).toISOString(), explorerTxUrl: url, status: 'confirmed' }
}
async function pages(url: string): Promise<any[]> {
  const all: any[] = []
  let next = new URL(url)
  for (let i = 0; i < MAX_PAGES; i++) {
    const page = await json(next.toString())
    const items = Array.isArray(page.items) ? page.items : []
    all.push(...items)
    const cursor = page.next_page_params
    if (!items.length || !cursor || typeof cursor !== 'object' || !Object.keys(cursor).length) return all
    const timestamps = items.map((x: any) => Date.parse(x.timestamp ?? x.transaction?.timestamp ?? '')).filter(Number.isFinite)
    if (timestamps.length && Math.min(...timestamps) < Date.now() - WINDOW) return all
    if (i === MAX_PAGES - 1) throw new Error('Ethereum history exceeded the pagination safety limit')
    next = new URL(url)
    for (const [k, v] of Object.entries(cursor)) if (v != null) next.searchParams.set(k, String(v))
  }
  return all
}
async function bitcoin(): Promise<Donation[]> {
  const base = `https://mempool.space/api/address/${BTC_ADDRESS}/txs`
  const [tip, batch] = await Promise.all([json('https://mempool.space/api/blocks/tip/height'), json(base)])
  const items = [...batch]
  let cursor = items.filter((x: any) => x.status?.confirmed).at(-1)
  for (let i = 1; cursor && i < MAX_PAGES; i++) {
    if (cursor.status.block_time * 1000 < Date.now() - WINDOW) break
    const next = await json(`${base}?after_txid=${cursor.txid}`)
    if (!next.length) break
    items.push(...next); cursor = next.filter((x: any) => x.status?.confirmed).at(-1)
    if (i === MAX_PAGES - 1 && cursor) throw new Error('Bitcoin history exceeded the pagination safety limit')
  }
  return items.flatMap((item: any) => {
    if (!item.status?.confirmed || tip - item.status.block_height + 1 < 3) return []
    const sat = item.vout.filter((o: any) => o.scriptpubkey_address === BTC_ADDRESS).reduce((s: bigint, o: any) => s + BigInt(o.value), 0n)
    if (!sat) return []
    const from = item.vin.find((v: any) => v.prevout?.scriptpubkey_address)?.prevout?.scriptpubkey_address
    return [makeTx('BTC', 'Bitcoin', item.txid, units(sat, 8), from, item.status.block_time * 1000, `https://mempool.space/tx/${item.txid}`)]
  })
}
async function ethereum(asset: 'ETH' | 'USDC'): Promise<Donation[]> {
  const base = `https://eth.blockscout.com/api/v2/addresses/${EVM_ADDRESS}`
  const [native, tokens, blocks] = await Promise.all([
    asset === 'ETH' ? pages(`${base}/transactions?filter=to`) : Promise.resolve([]),
    asset === 'USDC' ? pages(`${base}/token-transfers?type=ERC-20&filter=to&token=${USDC}`) : Promise.resolve([]),
    json('https://eth.blockscout.com/api/v2/blocks?type=block'),
  ])
  const height = Number(blocks.items?.[0]?.height ?? 0)
  if (asset === 'ETH') return native.flatMap((x: any) => {
    if (x.status !== 'ok' || x.to?.hash?.toLowerCase() !== EVM_ADDRESS.toLowerCase() || !x.block_number ||
      height - Number(x.block_number) + 1 < 12 || BigInt(x.value ?? 0) <= 0n) return []
    return [makeTx('ETH', 'Ethereum', x.hash, units(x.value, 18), x.from?.hash, Date.parse(x.timestamp), `https://etherscan.io/tx/${x.hash}`)]
  })
  return tokens.flatMap((x: any) => {
    const t = x.transaction
    if (x.token?.address?.toLowerCase() !== USDC.toLowerCase() || x.to?.hash?.toLowerCase() !== EVM_ADDRESS.toLowerCase() ||
      t?.status !== 'ok' || !x.block_number || height - Number(x.block_number) + 1 < 12) return []
    const raw = x.total?.value ?? x.value
    if (raw == null || BigInt(raw) <= 0n) return []
    return [makeTx('USDC', 'Ethereum (ERC-20)', t.hash, units(raw, Number(x.token?.decimals ?? 6)), x.from?.hash,
      Date.parse(x.timestamp ?? t.timestamp), `https://etherscan.io/tx/${t.hash}`, `:${x.log_index ?? x.index ?? ''}`)]
  })
}
async function tron(env: Env): Promise<Donation[]> {
  const url = new URL(`https://api.trongrid.io/v1/accounts/${TRON_ADDRESS}/transactions/trc20`)
  for (const [k, v] of Object.entries({ only_confirmed: 'true', limit: '200', contract_address: USDT, only_to: 'true', min_timestamp: String(Date.now() - WINDOW) })) url.searchParams.set(k, v)
  const headers = env.TRONGRID_API_KEY ? { 'TRON-PRO-API-KEY': env.TRONGRID_API_KEY } : {}
  const all: any[] = []
  for (let i = 0; i < MAX_PAGES; i++) {
    const result = await json(url.toString(), headers); all.push(...(result.data ?? []))
    const fingerprint = result.meta?.fingerprint
    if (!fingerprint || !result.data?.length) break
    if (i === MAX_PAGES - 1) throw new Error('TRON history exceeded the pagination safety limit')
    url.searchParams.set('fingerprint', fingerprint)
  }
  return all.flatMap((x: any) => {
    if (x.to !== TRON_ADDRESS || x.token_info?.address !== USDT || x.type !== 'Transfer' || !x.block_timestamp || BigInt(x.value ?? 0) <= 0n) return []
    return [makeTx('USDT', 'TRON (TRC-20)', x.transaction_id, units(x.value, Number(x.token_info.decimals ?? 6)), x.from,
      x.block_timestamp, `https://tronscan.org/#/transaction/${x.transaction_id}`)]
  })
}
async function donationAlerts(env: Env): Promise<Donation[]> {
  const response = await fetch('https://kubron.pages.dev/api/donationalerts/sync', {
    method: 'POST', headers: { accept: 'application/json', authorization: `Bearer ${env.DONATIONALERTS_SYNC_SECRET}` },
    signal: AbortSignal.timeout(20000),
  })
  if (!response.ok) throw new Error(`DonationAlerts sync endpoint returned HTTP ${response.status}`)
  const result = await response.json() as { data?: any[] }
  if (!Array.isArray(result.data)) throw new Error('Invalid DonationAlerts response')
  return result.data.flatMap(x => {
    const id = String(x.id ?? ''), asset = String(x.currency ?? '').trim().toUpperCase(), amount = String(x.amount ?? '')
    const time = Date.parse(String(x.created_at ?? '').includes('T') ? x.created_at : String(x.created_at ?? '').replace(' ', 'T') + 'Z')
    if (!id || !/^[A-Z]{3,8}$/.test(asset) || !/^[0-9]+([.][0-9]+)?$/.test(amount) || !Number.isFinite(time) || time > Date.now() || time < Date.now() - WINDOW || Number(amount) <= 0) return []
    const donor = typeof x.username === 'string' ? x.username.trim().slice(0, 100) : ''
    return [{ id: `donationalerts:${id}`, asset, network: 'DonationAlerts', amount, txHash: id, receivedAt: new Date(time).toISOString(),
      status: 'confirmed' as const, source: 'donationalerts', ...(donor ? { donorName: donor } : {}) }]
  })
}
async function save(db: D1Database, donations: Donation[]) {
  const statement = db.prepare(`INSERT OR IGNORE INTO donations
    (id, asset, network, amount, tx_hash, from_address, donor_name, received_at, explorer_tx_url, source, status)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'confirmed')`)
  for (let i = 0; i < donations.length; i += 100) {
    await db.batch(donations.slice(i, i + 100).map(x => statement.bind(x.id, x.asset, x.network, x.amount, x.txHash,
      x.fromAddress ?? null, x.donorName ?? null, x.receivedAt, x.explorerTxUrl ?? null, x.source ?? null)))
  }
}
async function sync(env: Env) {
  if (!env.DONATIONALERTS_SYNC_SECRET || env.DONATIONALERTS_SYNC_SECRET.length < 32) throw new Error('DONATIONALERTS_SYNC_SECRET is not configured')
  const batches = await Promise.all([bitcoin(), ethereum('ETH'), ethereum('USDC'), tron(env), donationAlerts(env)])
  await save(env.DB, batches.flat())
}
async function response(db: D1Database) {
  const since = new Date(Date.now() - WINDOW).toISOString()
  const { results } = await db.prepare(`SELECT id, asset, network, amount, tx_hash AS txHash, from_address AS fromAddress,
    donor_name AS donorName, received_at AS receivedAt, explorer_tx_url AS explorerTxUrl, source, status
    FROM donations WHERE status = 'confirmed' AND received_at >= ? AND received_at <= ?
    ORDER BY received_at DESC LIMIT 500`).bind(since, new Date().toISOString()).all()
  return Response.json({ schemaVersion: 1, updatedAt: new Date().toISOString(), wallets, transactions: results }, {
    headers: { 'cache-control': 'public, max-age=60, stale-while-revalidate=300', 'access-control-allow-origin': '*' },
  })
}
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url)
    if (request.method === 'GET' && url.pathname === '/api/donations') return response(env.DB)
    if (request.method === 'POST' && url.pathname === '/internal/sync') {
      const token = request.headers.get('authorization')?.replace(/^Bearer\s+/i, '') ?? ''
      if (!env.DONATIONALERTS_SYNC_SECRET || token !== env.DONATIONALERTS_SYNC_SECRET) return new Response('Unauthorized', { status: 401 })
      try { await sync(env); return Response.json({ ok: true }) }
      catch (error) { return Response.json({ error: error instanceof Error ? error.message : 'Sync failed' }, { status: 502 }) }
    }
    return new Response('Not found', { status: 404 })
  },
  async scheduled(_event: ScheduledEvent, env: Env, ctx: ExecutionContext) {
    ctx.waitUntil(sync(env))
  },
}
