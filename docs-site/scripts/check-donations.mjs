// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { readFile } from 'node:fs/promises'

const source = new URL('../public/donations.json', import.meta.url)
const data = JSON.parse(await readFile(source, 'utf8'))
const errors = []

const isHttpsUrl = (value) => {
  try {
    return new URL(value).protocol === 'https:'
  } catch {
    return false
  }
}

if (data.schemaVersion !== 1) errors.push('schemaVersion must be 1')
if (!Array.isArray(data.wallets)) errors.push('wallets must be an array')
if (!Array.isArray(data.transactions)) errors.push('transactions must be an array')
if (data.updatedAt !== null && (!Number.isFinite(Date.parse(data.updatedAt)))) errors.push('updatedAt must be null or a valid date')

const walletIds = new Set()
const walletKeys = new Set()
for (const [index, wallet] of (data.wallets ?? []).entries()) {
  const label = `wallets[${index}]`
  for (const field of ['id', 'asset', 'network', 'address']) {
    if (typeof wallet[field] !== 'string' || !wallet[field].trim()) errors.push(`${label}.${field} is required`)
  }
  if (walletIds.has(wallet.id)) errors.push(`${label}.id is duplicated`)
  walletIds.add(wallet.id)
  const key = `${wallet.asset}/${wallet.network}`
  if (walletKeys.has(key)) errors.push(`${label} duplicates asset/network ${key}`)
  walletKeys.add(key)
  if (wallet.explorerAddressUrl && !isHttpsUrl(wallet.explorerAddressUrl)) errors.push(`${label}.explorerAddressUrl must use HTTPS`)
  if (wallet.paymentUri && typeof wallet.paymentUri !== 'string') errors.push(`${label}.paymentUri must be a string`)
}

const transactionIds = new Set()
const transactionKeys = new Set()
for (const [index, tx] of (data.transactions ?? []).entries()) {
  const label = `transactions[${index}]`
  for (const field of ['id', 'asset', 'network', 'amount', 'txHash', 'receivedAt', 'explorerTxUrl']) {
    if (typeof tx[field] !== 'string' || !tx[field].trim()) errors.push(`${label}.${field} is required`)
  }
  if (!/^\\d+(\\.\\d+)?$/.test(tx.amount ?? '')) errors.push(`${label}.amount must be a decimal string`)
  if (!Number.isFinite(Date.parse(tx.receivedAt))) errors.push(`${label}.receivedAt must be a valid date`)
  if (!isHttpsUrl(tx.explorerTxUrl)) errors.push(`${label}.explorerTxUrl must use HTTPS`)
  if (tx.status !== 'confirmed') errors.push(`${label}.status must be confirmed`)
  if (transactionIds.has(tx.id)) errors.push(`${label}.id is duplicated`)
  transactionIds.add(tx.id)
  const key = `${tx.network}/${tx.txHash}`
  if (transactionKeys.has(key)) errors.push(`${label} duplicates transaction ${key}`)
  transactionKeys.add(key)
  if (tx.fromAddress !== undefined && typeof tx.fromAddress !== 'string') errors.push(`${label}.fromAddress must be a string`)
}

if (errors.length) {
  console.error('Invalid donations.json:')
  for (const error of errors) console.error(`- ${error}`)
  process.exitCode = 1
} else {
  console.log(`donations.json is valid (${data.wallets.length} wallets, ${data.transactions.length} transactions)`)
}
