<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ho-view">
    <header class="ho-head">
      <div>
         <h2>{{ $t('helm.operations.title') }}</h2>
        <p class="ho-sub">{{ $t('helm.operations.subtitle') }}</p>
      </div>
      <div class="ho-head-actions">
        <button class="btn" type="button" :disabled="loading" @click="load">{{ $t('helm.operations.refresh') }}</button>
        <button v-if="canClear" class="btn ho-danger" type="button" :disabled="!records.length" @click="clearAll">{{ $t('helm.operations.clear') }}</button>
      </div>
    </header>

    <section v-if="running.length" class="ho-running" :aria-label="$t('helm.operations.running')">
      <h3>{{ $t('helm.operations.running') }}</h3>
      <div v-for="o in running" :key="o.opId" class="ho-run">
        <span class="ho-spin" aria-hidden="true">⟳</span>
        <strong>{{ helmOpLabel(o.op) }} {{ o.release }}</strong>
        <span class="ho-muted">{{ o.namespace }} · {{ o.phase }}</span>
      </div>
    </section>

    <form class="ho-filters" @submit.prevent="load">
      <input v-model="search" class="form-input" type="search" :placeholder="$t('helm.operations.searchPlaceholder')" :aria-label="$t('helm.operations.searchAria')" />
      <select v-model="opFilter" class="form-input" :aria-label="$t('helm.operations.allOps')" @change="load">
        <option value="">{{ $t('helm.operations.allOps') }}</option>
        <option v-for="key in HELM_OPS" :key="key" :value="key">{{ helmOpLabel(key) }}</option>
      </select>
      <select v-model="statusFilter" class="form-input" :aria-label="$t('helm.operations.anyStatus')" @change="load">
        <option value="">{{ $t('helm.operations.anyStatus') }}</option>
        <option value="success">{{ $t('helm.operations.success') }}</option>
        <option value="failed">{{ $t('helm.operations.failed') }}</option>
        <option value="canceled">{{ $t('helm.operations.canceled') }}</option>
      </select>
    </form>

    <p v-if="error" class="form-error">{{ error }}</p>
    <p v-else-if="loaded && !records.length" class="ho-empty">{{ $t('helm.operations.noOps', { filtered }) }}</p>

    <table v-if="records.length" class="ho-table">
      <thead>
        <tr><th>{{ $t('helm.operations.time') }}</th><th>{{ $t('helm.operations.operation') }}</th><th>{{ $t('helm.operations.release') }}</th><th>{{ $t('helm.operations.cluster') }}</th><th>{{ $t('helm.operations.detail') }}</th><th>{{ $t('common.status') }}</th><th>{{ $t('helm.operations.duration') }}</th></tr>
      </thead>
      <tbody>
        <template v-for="r in records" :key="r.id">
          <tr class="ho-row" :class="{ open: openId === r.id }" tabindex="0" @click="toggle(r)" @keydown.enter="toggle(r)">
            <td class="ho-muted" :title="r.startedAt">{{ formatTime(r.startedAt) }}</td>
            <td>{{ helmOpLabel(r.op) }}</td>
            <td><strong>{{ r.release }}</strong><span class="ho-muted"> · {{ r.namespace }}</span></td>
            <td class="ho-muted">{{ r.cluster || $t('common.dash') }}</td>
            <td class="ho-desc" :title="r.detail">{{ r.detail }}<span v-if="r.revision" class="ho-muted"> #{{ r.revision }}</span></td>
            <td><span class="ho-badge" :class="r.status">{{ STATUS[r.status] || r.status }}</span></td>
            <td class="ho-muted">{{ formatDuration(r.durationMs) }}</td>
          </tr>
          <tr v-if="openId === r.id" class="ho-detail">
            <td colspan="7">
              <p v-if="detailLoading" class="ho-muted">{{ $t('helm.operations.loading') }}</p>
              <template v-else-if="detail">
                <pre v-if="detail.error" class="ho-log ho-err">{{ detail.error }}</pre>
                <pre v-if="detail.log && detail.log.length" class="ho-log">{{ detail.log.join('\n') }}</pre>
                <p v-else-if="!detail.error" class="ho-muted">{{ $t('helm.operations.emptyLog') }}</p>
              </template>
              <p v-else-if="detailError" class="form-error">{{ detailError }}</p>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { callApp } from '../../resources/wails'
import { askConfirm } from '../../resources/confirm'
import { can } from '../../composables/useAuth'
import { useNotifications } from '../../composables/useNotifications'
import { HELM_OPS, helmOpLabel, helmJournalTick, helmOps, startHelmOps } from '../../composables/useHelmOps'

const { t } = useI18n()
import { Perm } from '../../types/auth'
import { errorText } from '../../composables/errorText'

interface Rec {
  id: string; op: string; cluster: string; namespace: string; release: string; detail: string; revision: number
  status: string; error: string; startedAt: string; finishedAt: string; durationMs: number; log?: string[]
}

/** Подписи статусов: computed, чтобы обновлялись при смене языка */
const STATUS = computed<Record<string, string>>(() => ({ success: t('helm.operations.success'), failed: t('helm.operations.failed'), canceled: t('helm.operations.canceled') }))

const notifications = useNotifications()
const canClear = computed(() => can(Perm.ClusterManage))
const running = computed(() => helmOps.value.filter(o => !o.done))

const records = ref<Rec[]>([])
const loaded = ref(false)
const loading = ref(false)
const error = ref('')
const search = ref('')
const opFilter = ref('')
const statusFilter = ref('')
const filtered = computed(() => !!(search.value.trim() || opFilter.value || statusFilter.value))

const openId = ref('')
const detail = ref<Rec | null>(null)
const detailLoading = ref(false)
const detailError = ref('')

function formatTime(iso: string) {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function formatDuration(ms: number) {
  const s = Math.round(ms / 1000)
  if (s < 1) return '<1' + t('helm.ops_panel.sec')
  return s >= 60 ? `${Math.floor(s / 60)}${t('helm.ops_panel.min')} ${String(s % 60).padStart(2, '0')}${t('helm.ops_panel.sec')}` : `${s}${t('helm.ops_panel.sec')}`
}

let seq = 0
async function load() {
  const my = ++seq
  loading.value = true
  error.value = ''
  try {
    const r = (await callApp('ListHelmOperations', {
      op: opFilter.value, status: statusFilter.value, search: search.value, limit: 100,
    })) as Rec[]
    if (my !== seq) return
    records.value = r ?? []
    if (openId.value && !records.value.some(x => x.id === openId.value)) openId.value = ''
    loaded.value = true
  } catch (e: any) {
    if (my === seq) error.value = errorText(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

let dseq = 0
async function toggle(r: Rec) {
  if (openId.value === r.id) { openId.value = ''; return }
  openId.value = r.id
  detail.value = null
  detailError.value = ''
  detailLoading.value = true
  const my = ++dseq
  try {
    const d = (await callApp('GetHelmOperation', r.id)) as Rec
    if (my === dseq) detail.value = d
  } catch (e: any) {
    if (my === dseq) detailError.value = errorText(e)
  } finally {
    if (my === dseq) detailLoading.value = false
  }
}

async function clearAll() {
  if (!(await askConfirm(t('helm.operations.confirmClear')))) return
  try {
    await callApp('ClearHelmOperations')
    openId.value = ''
    notifications.notifySuccess(t('helm.operations.cleared'))
  } catch (e: any) {
    notifications.notifyError(t('helm.operations.clearError', { message: errorText(e) }))
  } finally {
    await load()
  }
}

let debounce: ReturnType<typeof setTimeout> | undefined
watch(search, () => { clearTimeout(debounce); debounce = setTimeout(load, 300) })
watch(helmJournalTick, load) // операция завершилась — в журнале новая запись
onMounted(() => { startHelmOps(); load() })
onBeforeUnmount(() => clearTimeout(debounce))
</script>

<style scoped>
.ho-view { padding: 24px; text-align: left; max-width: 1300px; }
.ho-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 16px; }
.ho-head h2 { margin: 0; font-size: 22px; color: var(--accent); }
.ho-sub { margin: 4px 0 0; color: var(--text-muted); font-size: 13px; }
.ho-head-actions { display: flex; gap: 8px; }
.ho-danger { color: var(--danger); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.ho-running { margin-bottom: 16px; padding: 10px 12px; border: 1px solid var(--surface-0); border-radius: 8px; }
.ho-running h3 { margin: 0 0 6px; font-size: 13px; color: var(--text); }
.ho-run { display: flex; align-items: center; gap: 8px; font-size: 13px; padding: 3px 0; }
.ho-spin { color: var(--accent); }
.ho-filters { display: grid; grid-template-columns: 2fr 1fr 1fr; gap: 10px; max-width: 800px; margin-bottom: 14px; }
.ho-filters .form-input { width: 100%; box-sizing: border-box; padding: 8px 10px; }
.ho-empty { color: var(--text-subtle); font-size: 13px; }
.ho-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.ho-table th { text-align: left; padding: 8px 10px; color: var(--text-muted); font-weight: 600; border-bottom: 1px solid var(--surface-0); }
.ho-table td { padding: 8px 10px; border-bottom: 1px solid #232334; vertical-align: top; }
.ho-row { cursor: pointer; }
.ho-row:hover, .ho-row.open { background: rgba(var(--accent-rgb), 0.08); }
.ho-muted { color: var(--text-subtle); }
.ho-desc { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ho-badge { padding: 1px 8px; border-radius: 10px; font-size: 12px; background: var(--surface-0); }
.ho-badge.success { color: var(--ok); background: rgba(166, 227, 161, 0.12); }
.ho-badge.failed { color: var(--danger); background: rgba(var(--danger-rgb), 0.12); }
.ho-badge.canceled { color: var(--warn); background: rgba(249, 226, 175, 0.12); }
.ho-detail td { background: var(--bg-sunken); }
.ho-log { margin: 0 0 8px; padding: 8px 10px; background: #12141f; border-radius: 4px; font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 11px; line-height: 1.45; color: var(--gray-400); white-space: pre-wrap; word-break: break-word; max-height: 320px; overflow: auto; }
.ho-err { color: var(--red-soft); }
</style>
