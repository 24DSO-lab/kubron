// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
const DATA_PATH = fileURLToPath(new URL('../public/donations.json', import.meta.url))
const BTC_CONFIRMATIONS = Number(process.env.BTC_CONFIRMATIONS ?? 3)
const EVM_CONFIRMATIONS = Number(process.env.EVM_CONFIRMATIONS ?? 12)
const SCAN_WINDOW_MS = 30 * 24 * 60 * 60 * 1000
const MAX_SCAN_PAGES = 20
const TRONGRID_API_KEY = process.env.TRONGRID_API_KEY ?? ''
const USDC = '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48'
const USDT = 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t'
const fetchJson = async (url, headers = {}) => {
  const res = await fetch(url, { headers: { Accept: 'application/json', ...headers }, signal: AbortSignal.timeout(20000) })
  if (!res.ok) throw new Error(`HTTP ${res.status} from ${new URL(url).host}`)
  return res.json()
}
export const decimalFromUnits = (units, decimals) => {
  const n = BigInt(units), base = 10n ** BigInt(decimals), whole = n / base
  const fraction = (n % base).toString().padStart(decimals, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole.toString()
}
const tx = (asset, network, hash, amount, fromAddress, time, explorer, suffix = '') => ({
  id: `${network}:${hash.toLowerCase()}${suffix}`, asset, network, amount, txHash: hash,
  ...(fromAddress ? { fromAddress } : {}), receivedAt: new Date(time).toISOString(), explorerTxUrl: explorer, status: 'confirmed'
})
async function donationAlerts() {
  const secret = process.env.DONATIONALERTS_SYNC_SECRET ?? ''
  if (!secret) return []
  const endpoint = process.env.DONATIONALERTS_SYNC_URL ?? 'https://kubron.pages.dev/api/donationalerts/sync'
  const response = await fetch(endpoint, { method: 'POST', headers: { Accept: 'application/json', Authorization: `Bearer ${secret}` }, signal: AbortSignal.timeout(20000) })
  if (!response.ok) throw new Error(`DonationAlerts sync endpoint returned HTTP ${response.status}`)
  const result = await response.json()
  if (!Array.isArray(result.data)) throw new Error('Invalid DonationAlerts sync response')
  return result.data.flatMap(item => {
    const id = String(item.id ?? '')
    const currency = String(item.currency ?? '').trim().toUpperCase()
    const amount = String(item.amount ?? '')
    const createdAt = String(item.created_at ?? '')
    const timestamp = Date.parse(createdAt.includes('T') ? createdAt : createdAt.replace(' ', 'T') + 'Z')
    if (!id || !/^[A-Z]{3,8}$/.test(currency) || !/^[0-9]+([.][0-9]+)?$/.test(amount) ||
      !Number.isFinite(timestamp) || Number(amount) <= 0) return []
    const donorName = typeof item.username === 'string' ? item.username.trim().slice(0, 100) : ''
    return [{
      id: `donationalerts:${id}`, asset: currency, network: 'DonationAlerts', amount,
      txHash: id, receivedAt: new Date(timestamp).toISOString(), status: 'confirmed',
      source: 'donationalerts', ...(donorName ? { donorName } : {}),
    }]
  })
}

async function fetchBitcoinHistory(initialUrl) {
  const items = []
  let url = new URL(initialUrl)
  for (let page = 0; page < MAX_SCAN_PAGES; page++) {
    const batch = await fetchJson(url)
    if (!Array.isArray(batch) || batch.length === 0) break
    items.push(...batch)
    const confirmed = batch.filter(item => item.status?.confirmed && Number.isFinite(item.status.block_time))
    if (confirmed.some(item => item.status.block_time * 1000 < Date.now() - SCAN_WINDOW_MS)) break
    const lastConfirmed = confirmed.at(-1)
    if (!lastConfirmed || confirmed.length < 25) break
    url = new URL(initialUrl)
    url.searchParams.set('after_txid', lastConfirmed.txid)
  }
  return items
}

async function fetchBlockscoutHistory(initialUrl) {
  const items = []
  let url = new URL(initialUrl)
  for (let page = 0; page < MAX_SCAN_PAGES; page++) {
    const result = await fetchJson(url)
    const batch = Array.isArray(result.items) ? result.items : []
    if (!batch.length) break
    items.push(...batch)
    const timestamps = batch.map(item => Date.parse(item.timestamp ?? item.transaction?.timestamp ?? '')).filter(Number.isFinite)
    if (timestamps.length && Math.min(...timestamps) < Date.now() - SCAN_WINDOW_MS) break
    const cursor = result.next_page_params
    if (!cursor || typeof cursor !== 'object' || !Object.keys(cursor).length) break
    url = new URL(initialUrl)
    for (const [key, value] of Object.entries(cursor)) {
      if (value !== null && value !== undefined) url.searchParams.set(key, String(value))
    }
  }
  return items
}

async function bitcoin(wallet) {
  const addressUrl = `https://mempool.space/api/address/${encodeURIComponent(wallet.address)}/txs`
  const [tip, items] = await Promise.all([
    fetchJson('https://mempool.space/api/blocks/tip/height'),
    fetchBitcoinHistory(addressUrl),
  ])
  return items.flatMap(item => {
    if (!item.status?.confirmed || tip - item.status.block_height + 1 < BTC_CONFIRMATIONS) return []
    const sat = item.vout.filter(o => o.scriptpubkey_address === wallet.address).reduce((s, o) => s + BigInt(o.value), 0n)
    if (!sat) return []
    const from = item.vin.find(i => i.prevout?.scriptpubkey_address)?.prevout?.scriptpubkey_address
    return [tx('BTC', 'Bitcoin', item.txid, decimalFromUnits(sat, 8), from, item.status.block_time * 1000,
      `https://mempool.space/tx/${item.txid}`)]
  })
}
async function ethereum(wallet, asset) {
  const addr = wallet.address, base = `https://eth.blockscout.com/api/v2/addresses/${encodeURIComponent(addr)}`
  const [native, tokens, blocks] = await Promise.all([
    asset === 'ETH' ? fetchBlockscoutHistory(`${base}/transactions?filter=to`) : Promise.resolve([]),
    asset === 'USDC' ? fetchBlockscoutHistory(`${base}/token-transfers?type=ERC-20&filter=to&token=${USDC}`) : Promise.resolve([]),
    fetchJson('https://eth.blockscout.com/api/v2/blocks?type=block')
  ])
  const height = Number(blocks.items?.[0]?.height ?? 0)
  if (asset === 'ETH') return native.flatMap(item => {
    if (item.status !== 'ok' || item.to?.hash?.toLowerCase() !== addr.toLowerCase() || !item.block_number ||
      height - Number(item.block_number) + 1 < EVM_CONFIRMATIONS || BigInt(item.value ?? 0) <= 0n) return []
    return [tx('ETH', 'Ethereum', item.hash, decimalFromUnits(item.value, 18), item.from?.hash, Date.parse(item.timestamp),
      `https://etherscan.io/tx/${item.hash}`)]
  })
  return tokens.flatMap(item => {
    const t = item.transaction, contract = item.token?.address?.toLowerCase()
    if (contract !== USDC.toLowerCase() || item.to?.hash?.toLowerCase() !== addr.toLowerCase() || t?.status !== 'ok' ||
      !item.block_number || height - Number(item.block_number) + 1 < EVM_CONFIRMATIONS) return []
    const raw = item.total?.value ?? item.value
    if (raw == null || BigInt(raw) <= 0n) return []
    return [tx('USDC', 'Ethereum (ERC-20)', t.hash, decimalFromUnits(raw, Number(item.token?.decimals ?? 6)),
      item.from?.hash, Date.parse(item.timestamp ?? t.timestamp), `https://etherscan.io/tx/${t.hash}`, `:${item.log_index ?? item.index ?? ''}`)]
  })
}
async function tron(wallet) {
  const u = new URL(`https://api.trongrid.io/v1/accounts/${encodeURIComponent(wallet.address)}/transactions/trc20`)
  for (const [k, v] of Object.entries({
    only_confirmed: 'true', limit: '200', contract_address: USDT, only_to: 'true',
    min_timestamp: String(Date.now() - SCAN_WINDOW_MS),
  })) u.searchParams.set(k, v)
  const headers = TRONGRID_API_KEY ? { 'TRON-PRO-API-KEY': TRONGRID_API_KEY } : {}
  const items = []
  for (let page = 0; page < MAX_SCAN_PAGES; page++) {
    const result = await fetchJson(u, headers)
    items.push(...(result.data ?? []))
    const fingerprint = result.meta?.fingerprint
    if (!fingerprint || !result.data?.length) break
    u.searchParams.set('fingerprint', fingerprint)
  }
  return items.flatMap(item => {
    if (item.to !== wallet.address || item.token_info?.address !== USDT || item.type !== 'Transfer' || !item.block_timestamp || BigInt(item.value ?? 0) <= 0n) return []
    return [tx('USDT', 'TRON (TRC-20)', item.transaction_id, decimalFromUnits(item.value, Number(item.token_info.decimals ?? 6)),
      item.from, item.block_timestamp, `https://tronscan.org/#/transaction/${item.transaction_id}`)]
  })
}
export async function syncDonations({ dryRun = false } = {}) {
  const data = JSON.parse(await readFile(DATA_PATH, 'utf8'))
  if (data.schemaVersion !== 1 || !Array.isArray(data.wallets) || !Array.isArray(data.transactions)) throw new Error('Invalid donations data schema')
  const sources = { 'btc-bitcoin': bitcoin, 'eth-ethereum': w => ethereum(w, 'ETH'), 'usdc-ethereum': w => ethereum(w, 'USDC'), 'usdt-tron': tron }
  const incoming = []
  for (const w of data.wallets) {
    if (!sources[w.id]) throw new Error(`Unknown wallet id: ${w.id}`)
    incoming.push(...await sources[w.id](w))
  }
  incoming.push(...await donationAlerts())
  const ids = new Set(data.transactions.map(x => x.id))
  const fresh = incoming.filter(x => !ids.has(x.id))
  data.transactions = [...data.transactions, ...fresh].sort((a, b) => Date.parse(b.receivedAt) - Date.parse(a.receivedAt))
  data.updatedAt = new Date().toISOString()
  if (!dryRun) await writeFile(DATA_PATH, JSON.stringify(data, null, 2) + '\n')
  return { added: fresh.length, total: data.transactions.length, data }
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const r = await syncDonations({ dryRun: process.argv.includes('--dry-run') })
    console.log(`Donation sync complete: ${r.added} new / ${r.total} total${process.argv.includes('--dry-run') ? ' (dry run)' : ''}`)
  } catch (e) { console.error('Donation sync failed:', e); process.exitCode = 1 }
}
