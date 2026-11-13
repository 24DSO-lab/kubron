<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { withBase } from 'vitepress'

type Wallet = {
  id: string
  asset: string
  network: string
  address: string
  explorerAddressUrl?: string
  paymentUri?: string
}

type Transaction = {
  id: string
  asset: string
  network: string
  amount: string
  txHash: string
  fromAddress?: string
  receivedAt: string
  explorerTxUrl: string
  status: 'confirmed'
}

type DonationData = {
  schemaVersion: number
  updatedAt: string | null
  wallets: Wallet[]
  transactions: Transaction[]
}

const data = ref<DonationData | null>(null)
const loading = ref(true)
const error = ref('')
const filter = ref('all')
const copied = ref('')

onMounted(async () => {
  try {
    const response = await fetch(withBase('/donations.json'), { headers: { Accept: 'application/json' } })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const payload = await response.json() as DonationData
    if (payload.schemaVersion !== 1 || !Array.isArray(payload.wallets) || !Array.isArray(payload.transactions)) {
      throw new Error('Unsupported donations data format')
    }
    data.value = payload
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'Unable to load contribution data'
  } finally {
    loading.value = false
  }
})

const wallets = computed(() => (data.value?.wallets ?? []).filter(wallet => wallet.address.trim()))
const assets = computed(() => [...new Set((data.value?.transactions ?? []).map(tx => tx.asset))].sort())
const transactions = computed(() => {
  const rows = data.value?.transactions ?? []
  return [...rows]
    .filter(tx => filter.value === 'all' || tx.asset === filter.value)
    .sort((a, b) => Date.parse(b.receivedAt) - Date.parse(a.receivedAt))
})

function shortAddress(value: string): string {
  if (value.length <= 18) return value
  return `${value.slice(0, 9)}…${value.slice(-7)}`
}

function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' }).format(date) + ' UTC'
}

async function copyAddress(wallet: Wallet) {
  try {
    await navigator.clipboard.writeText(wallet.address)
    copied.value = wallet.id
  } catch {
    copied.value = ''
  }
}
</script>

<template>
  <div class="donations-panel">
    <section class="donations-section" aria-labelledby="donation-wallets-title">
      <h3 id="donation-wallets-title">Crypto wallets</h3>
      <p class="donations-muted">Choose the exact asset and network shown below. Sending an asset over a different network may result in a permanent loss.</p>
      <p v-if="loading" class="donations-status" role="status">Loading wallet configuration…</p>
      <p v-else-if="error" class="donations-empty" role="alert">Wallet configuration is temporarily unavailable. {{ error }}</p>
      <p v-else-if="wallets.length === 0" class="donations-empty">Wallet addresses are being configured. No deposit address is published yet.</p>
      <div v-else class="donations-wallets">
        <article v-for="wallet in wallets" :key="wallet.id" class="donations-wallet">
          <div>
            <strong>{{ wallet.asset }} · {{ wallet.network }}</strong>
            <code>{{ wallet.address }}</code>
          </div>
          <div class="donations-actions">
            <button type="button" @click="copyAddress(wallet)">{{ copied === wallet.id ? 'Copied' : 'Copy address' }}</button>
            <a v-if="wallet.explorerAddressUrl" :href="wallet.explorerAddressUrl" target="_blank" rel="noopener noreferrer">Explorer ↗</a>
            <a v-if="wallet.paymentUri" :href="wallet.paymentUri">Open wallet</a>
          </div>
        </article>
      </div>
    </section>

    <section class="donations-section" aria-labelledby="donations-ledger-title">
      <div class="donations-ledger-head">
        <div>
          <h3 id="donations-ledger-title">Public contribution ledger</h3>
          <p class="donations-muted">Confirmed on-chain transfers are listed here with their transaction and sender addresses. A blockchain address is not a verified personal identity.</p>
        </div>
        <label>
          <span class="vp-sr-only">Filter by asset</span>
          <select v-model="filter" class="donations-filter">
            <option value="all">All assets</option>
            <option v-for="asset in assets" :key="asset" :value="asset">{{ asset }}</option>
          </select>
        </label>
      </div>
      <p v-if="loading" class="donations-status" role="status">Loading ledger…</p>
      <p v-else-if="error" class="donations-empty" role="alert">The public ledger is temporarily unavailable.</p>
      <p v-else-if="transactions.length === 0" class="donations-empty">No confirmed contributions have been published yet.</p>
      <div v-else class="donations-table-wrap">
        <table class="donations-table">
          <thead><tr><th>Date (UTC)</th><th>Asset / network</th><th>Amount</th><th>From</th><th>Transaction</th></tr></thead>
          <tbody>
            <tr v-for="tx in transactions" :key="tx.id">
              <td>{{ formatDate(tx.receivedAt) }}</td>
              <td>{{ tx.asset }} · {{ tx.network }}</td>
              <td>{{ tx.amount }}</td>
              <td :title="tx.fromAddress || 'Not provided'">{{ tx.fromAddress ? shortAddress(tx.fromAddress) : '—' }}</td>
              <td><a :href="tx.explorerTxUrl" target="_blank" rel="noopener noreferrer">{{ shortAddress(tx.txHash) }} ↗</a></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="data?.updatedAt" class="donations-status">Ledger updated: {{ formatDate(data.updatedAt) }}</p>
    </section>
  </div>
</template>
