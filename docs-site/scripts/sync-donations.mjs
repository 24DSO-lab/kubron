// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
const DATA_PATH = fileURLToPath(new URL('../public/donations.json', import.meta.url))
const BTC_CONFIRMATIONS = Number(process.env.BTC_CONFIRMATIONS ?? 3)
const EVM_CONFIRMATIONS = Number(process.env.EVM_CONFIRMATIONS ?? 12)
const TRONGRID_API_KEY = process.env.TRONGRID_API_KEY ?? ''
const USDC = '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48'
const USDT = 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t'
const fetchJson = async (url, headers = {}) => {
  const res = await fetch(url, { headers: { Accept: 'application/json', ...headers }, signal: AbortSignal.timeout(20000 })
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
async function bitcoin(wallet) {
  const [items, tip] = await Promise.all([
    fetchJson(`https://mempool.space/api/address/${encodeURIComponent(wallet.address)}/txs`),
    fetchJson('https://mempool.space/api/blocks/tip/height')
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
    fetchJson(`${base}/transactions?filter=to`),
    fetchJson(`${base}/token-transfers?type=ERC-20&filter=to&token=${USDC}`),
    fetchJson('https://eth.blockscout.com/api/v2/blocks?type=block')
  ])
  const height = Number(blocks.items?.[0]?.height ?? 0)
  if (asset === 'ETH') return (native.items ?? []).flatMap(item => {
    if (item.status !== 'ok' || item.to?.hash?.toLowerCase() !== addr.toLowerCase() || !item.block_number ||
      height - Number(item.block_number) + 1 < EVM_CONFIRMATIONS || BigInt(item.value ?? 0) <= 0n) return []
    return [tx('ETH', 'Ethereum', item.hash, decimalFromUnits(item.value, 18), item.from?.hash, Date.parse(item.timestamp),
      `https://etherscan.io/tx/${item.hash}`)]
  })
  return (tokens.items ?? []).flatMap(item => {
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
  for (const [k, v] of Object.entries({ only_confirmed: 'true', limit: '200', contract_address: USDT, only_to: 'true' })) u.searchParams.set(k, v)
  const headers = TRONGRID_API_KEY ? { 'TRON-PRO-API-KEY': TRONGRID_API_KEY } : {}
  const result = await fetchJson(u, headers)
  return (result.data ?? []).flatMap(item => {
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
