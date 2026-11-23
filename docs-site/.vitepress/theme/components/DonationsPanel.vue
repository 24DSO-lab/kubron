<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useData, withBase } from 'vitepress'

type Wallet = {
  id: string
  asset: string
  network: string
  address: string
  explorerAddressUrl?: string
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

const { lang } = useData()
const isRussian = computed(() => lang.value.toLowerCase().startsWith('ru'))

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
const recentTransactions = computed(() => {
  const cutoff = Date.now() - 30 * 24 * 60 * 60 * 1000
  return (data.value?.transactions ?? []).filter(tx => {
    const receivedAt = Date.parse(tx.receivedAt)
    return tx.status === 'confirmed' && Number.isFinite(receivedAt) && receivedAt >= cutoff && receivedAt <= Date.now()
  })
})
const assets = computed(() => [...new Set(recentTransactions.value.map(tx => tx.asset))].sort())
const transactions = computed(() => [...recentTransactions.value]
  .filter(tx => filter.value === 'all' || tx.asset === filter.value)
  .sort((a, b) => Date.parse(b.receivedAt) - Date.parse(a.receivedAt)))

function shortAddress(value: string): string {
  if (value.length <= 18) return value
  return `${value.slice(0, 9)}…${value.slice(-7)}`
}

function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(isRussian.value ? 'ru-RU' : 'en', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' }).format(date) + ' UTC'
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
      <h3 id="donation-wallets-title">{{ isRussian ? 'Криптовалютные кошельки' : 'Crypto wallets' }}</h3>
      <p class="donations-muted">{{ isRussian ? 'Выбирайте строго указанную ниже монету и сеть. Отправка средств через другую сеть может привести к их безвозвратной потере.' : 'Choose the exact asset and network shown below. Sending an asset over a different network may result in a permanent loss.' }}</p>
      <p v-if="loading" class="donations-status" role="status">{{ isRussian ? 'Загрузка настроек кошельков…' : 'Loading wallet configuration…' }}</p>
      <p v-else-if="error" class="donations-empty" role="alert">{{ isRussian ? 'Настройки кошельков временно недоступны.' : 'Wallet configuration is temporarily unavailable.' }} {{ error }}</p>
      <p v-else-if="wallets.length === 0" class="donations-empty">{{ isRussian ? 'Адреса кошельков настраиваются. Адреса для перевода пока не опубликованы.' : 'Wallet addresses are being configured. No deposit address is published yet.' }}</p>
      <div v-else class="donations-wallets">
        <article v-for="wallet in wallets" :key="wallet.id" class="donations-wallet">
          <div class="donations-wallet-info">
            <strong>{{ wallet.asset }} · {{ wallet.network }}</strong>
            <code>{{ wallet.address }}</code>
            <div class="donations-actions">
              <button type="button" @click="copyAddress(wallet)">{{ copied === wallet.id ? (isRussian ? 'Скопировано' : 'Copied') : (isRussian ? 'Копировать адрес' : 'Copy address') }}</button>
              <a v-if="wallet.explorerAddressUrl" :href="wallet.explorerAddressUrl" target="_blank" rel="noopener noreferrer">{{ isRussian ? 'Обозреватель ↗' : 'Explorer ↗' }}</a>
            </div>
          </div>
          <div class="donations-qr-wrap">
            <img class="donations-qr" :src="withBase(`/qr/${wallet.id}.svg`)" :alt="isRussian ? `QR-код для ${wallet.asset} в сети ${wallet.network}` : `QR code for ${wallet.asset} on ${wallet.network}`" width="144" height="144">
            <span>{{ isRussian ? 'Сканируйте кошельком' : 'Scan with your wallet' }}</span>
          </div>
        </article>
      </div>
    </section>

    <section class="donations-section" aria-labelledby="donations-ledger-title">
      <div class="donations-ledger-head">
        <div>
          <h3 id="donations-ledger-title">{{ isRussian ? 'Поступления за последние 30 дней' : 'Contributions in the last 30 days' }}</h3>
          <p class="donations-muted">{{ isRussian ? 'Здесь отображаются только подтверждённые переводы за последние 30 дней.' : 'This list shows only confirmed transfers from the last 30 days.' }}</p>
        </div>
        <label v-if="recentTransactions.length > 0">
          <span class="vp-sr-only">{{ isRussian ? 'Фильтр по активу' : 'Filter by asset' }}</span>
          <select v-model="filter" class="donations-filter">
            <option value="all">{{ isRussian ? 'Все активы' : 'All assets' }}</option>
            <option v-for="asset in assets" :key="asset" :value="asset">{{ asset }}</option>
          </select>
        </label>
      </div>
      <p v-if="loading" class="donations-status" role="status">{{ isRussian ? 'Загрузка реестра…' : 'Loading ledger…' }}</p>
      <p v-else-if="error" class="donations-empty" role="alert">{{ isRussian ? 'Публичный реестр временно недоступен.' : 'The public ledger is temporarily unavailable.' }}</p>
      <p v-else-if="transactions.length === 0" class="donations-empty">{{ isRussian ? 'Пока нет поступлений за последние 30 дней. Станьте одним из первых спонсоров Kubron.' : 'There are no contributions in the last 30 days yet. Be one of the first Kubron sponsors.' }}</p>
      <div v-else class="donations-table-wrap">
        <table class="donations-table">
          <thead><tr><th>{{ isRussian ? 'Дата (UTC)' : 'Date (UTC)' }}</th><th>{{ isRussian ? 'Актив / сеть' : 'Asset / network' }}</th><th>{{ isRussian ? 'Сумма' : 'Amount' }}</th><th>{{ isRussian ? 'Отправитель' : 'From' }}</th><th>{{ isRussian ? 'Транзакция' : 'Transaction' }}</th></tr></thead>
          <tbody>
            <tr v-for="tx in transactions" :key="tx.id">
              <td>{{ formatDate(tx.receivedAt) }}</td>
              <td>{{ tx.asset }} · {{ tx.network }}</td>
              <td>{{ tx.amount }}</td>
              <td :title="tx.fromAddress || (isRussian ? 'Не указан' : 'Not provided')">{{ tx.fromAddress ? shortAddress(tx.fromAddress) : '—' }}</td>
              <td><a :href="tx.explorerTxUrl" target="_blank" rel="noopener noreferrer">{{ shortAddress(tx.txHash) }} ↗</a></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="data?.updatedAt" class="donations-status">{{ isRussian ? 'Реестр обновлён:' : 'Ledger updated:' }} {{ formatDate(data.updatedAt) }}</p>
    </section>
  </div>
</template>
