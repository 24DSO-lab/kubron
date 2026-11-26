<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="audit-view">
    <header class="audit-head">
      <div>
        <h2>{{ $t('audit.title') }}</h2>
        <p class="audit-sub">{{ $t('audit.subtitle') }}</p>
      </div>
      <div class="audit-actions">
        <template v-if="capabilities.teams">
          <button class="btn btn-secondary" type="button" :disabled="loading || exporting" @click="exportLog('csv')">
            {{ $t('audit.export.csv') }}
          </button>
          <button class="btn btn-secondary" type="button" :disabled="loading || exporting" @click="exportLog('json')">
            {{ $t('audit.export.json') }}
          </button>
        </template>
        <button class="btn" type="button" :disabled="loading" @click="load">{{ $t('audit.refresh') }}</button>
      </div>
    </header>

    <p v-if="page?.writeError" class="audit-warn">
      {{ $t('audit.writeError', { error: page.writeError }) }}
    </p>

    <form class="audit-filters" @submit.prevent="load">
      <input v-model="filter.user" class="form-input" type="search" 
        :placeholder="$t('audit.filters.user')" :aria-label="$t('audit.filters.user')" />
      <input v-model="filter.action" class="form-input" type="search" :placeholder="$t('audit.filters.actionPlaceholder')"
        :aria-label="$t('audit.filters.action')" />
      <input v-model="filter.text" class="form-input" type="search" :placeholder="$t('audit.filters.objectPlaceholder')"
        :aria-label="$t('audit.filters.object')" />
      <select v-model="filter.result" class="form-input" 
        :aria-label="$t('audit.filters.result')">
        <option value="">{{ $t('audit.filters.anyResult') }}</option>
        <option v-for="id in RESULT_IDS" :key="id" :value="id">{{ resultLabel(id) }}</option>
      </select>
      <template v-if="capabilities.teams">
        <select v-model="filter.team" class="form-input" :aria-label="$t('audit.filters.team')">
          <option value="">{{ $t('audit.filters.anyTeam') }}</option>
          <option v-for="tm in teams" :key="tm.id" :value="tm.id">{{ tm.name }}</option>
        </select>
        <select v-model="filter.cluster" class="form-input" :aria-label="$t('audit.filters.cluster')">
          <option value="">{{ $t('audit.filters.anyCluster') }}</option>
          <option v-for="c in clusterOptions" :key="c.id" :value="c.id">{{ c.displayName }}</option>
        </select>
      </template>
      <select v-model="period" class="form-input" 
        :aria-label="$t('audit.filters.period')">
        <option v-for="p in PERIODS" :key="p.id" :value="p.id">{{ $t(`audit.periods.${p.id}`) }}</option>
      </select>
      <select v-model.number="filter.limit" class="form-input" 
        :aria-label="$t('audit.filters.limit')">
        <option v-for="n in LIMITS" :key="n" :value="n">{{ $t('audit.filters.show', { n }) }}</option>
      </select>
    </form>

    <p v-if="loadError" class="form-error">{{ loadError }}</p>

    <p v-if="loading && !page" class="audit-empty">{{ $t('audit.loading') }}</p>
    <p v-else-if="page && !page.events.length && !loadError" class="audit-empty">{{ $t('audit.empty') }}</p>

    <table v-if="page?.events.length" class="audit-table">
      <thead>
        <tr>
          <th>{{ $t('audit.columns.time') }}</th>
          <th>{{ $t('audit.columns.user') }}</th>
          <th>{{ $t('audit.columns.action') }}</th>
          <th>{{ $t('audit.columns.result') }}</th>
          <th v-if="capabilities.teams">{{ $t('audit.columns.scope') }}</th>
          <th>{{ $t('audit.columns.detail') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(e, i) in page.events" :key="i" :class="{ denied: e.result === 'denied' }">
          <td class="nowrap muted">{{ formatTime(e.time) }}</td>
          <td class="nowrap">
            <template v-if="e.user">
              <strong>{{ e.user }}</strong>
              <span v-if="e.role" class="role-tag">{{ roleLabel(e.role) }}</span>
            </template>
            <span v-else class="muted">{{ $t('common.dash') }}</span>
          </td>
          <td :title="e.action">{{ actionLabel(e.action) }}</td>
          <td>
            <span :class="['badge', `badge-${e.result}`]">{{ resultLabel(e.result) }}</span>
          </td>
          <td v-if="capabilities.teams" class="nowrap">
            <template v-if="e.teamId || e.clusterId">
              {{ teamName(e.teamId) }}<span v-if="e.clusterId" class="muted"> / {{ clusterName(e.clusterId) }}</span>
            </template>
            <span v-else class="muted">{{ $t('common.dash') }}</span>
            <div v-if="e.source" class="muted src">{{ e.source }}</div>
          </td>
          <td class="detail">{{ e.detail }}</td>
        </tr>
      </tbody>
    </table>

    <p v-if="page?.truncated" class="audit-more">
      {{ $t('audit.truncated', { n: page.events.length }) }}
    </p>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { callApp } from '../../resources/wails'
import { errorText } from '../../composables/errorText'
import { useCapabilities } from '../../composables/useCapabilities'
import { useTeams } from '../../composables/useTeams'
import { useClusters } from '../../composables/useClusters'
import { useNotifications } from '../../composables/useNotifications'
import { downloadTextFile } from '../../platform/files'
import { formatDateTime } from '../../i18n/format'
import { te, t } from '../../i18n'
import { RESULT_IDS, resultLabel, actionLabel, type AuditFilter, type AuditPage } from '../../types/audit'

const HOUR = 3600_000
const PERIODS = [
  { id: 'all', ms: 0 },
  { id: '1h', ms: HOUR },
  { id: '24h', ms: 24 * HOUR },
  { id: '7d', ms: 7 * 24 * HOUR },
  { id: '30d', ms: 30 * 24 * HOUR },
]
const LIMITS = [200, 500, 1000]

const { capabilities } = useCapabilities()
const { teams, loadTeams } = useTeams()
const { clusters, loadClusters } = useClusters()
const notifications = useNotifications()

const filter = reactive({ user: '', action: '', text: '', result: '', limit: LIMITS[0], team: '', cluster: '' })
const exporting = ref(false)

// Имена вместо идентификаторов; удалённые команды и кластеры остаются в журнале, поэтому запасной вариант — идентификатор
const teamName = (id?: string) => (id ? teams.value.find(x => x.id === id)?.name ?? id : t('common.dash'))
const clusterName = (id: string) => clusters.value.find(c => c.id === id)?.displayName ?? id
// При выбранной команде в списке кластеров — только её кластеры
const clusterOptions = computed(() => clusters.value.filter(c => !filter.team || c.teamId === filter.team))
const period = ref('all')
const page = ref<AuditPage | null>(null)
const loading = ref(false)
const loadError = ref('')

/** Роль: подпись из roles.*, иначе значение бэкенда как есть */
function roleLabel(role: string): string {
  return te(`roles.${role}`) ? t(`roles.${role}`) : role
}

function formatTime(iso: string): string {
  return formatDateTime(iso) || t('common.dash')
}

// Ответы могут прийти не в порядке отправки (быстрый ввод в фильтр): применяем только последний.
let seq = 0

async function load() {
  const my = ++seq
  loading.value = true
  loadError.value = ''
  const request = requestFilter()
  try {
    const result = (await callApp('GetAuditLog', request)) as AuditPage
    if (my !== seq) return
    page.value = { ...result, events: result.events ?? [] }
  } catch (e) {
    if (my !== seq) return
    loadError.value = errorText(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

// Текстовые поля — с задержкой, чтобы не дёргать бэкенд на каждую букву; списки — сразу.
let timer: ReturnType<typeof setTimeout> | undefined
watch(
  () => [filter.user, filter.action, filter.text],
  () => {
    clearTimeout(timer)
    timer = setTimeout(load, 300)
  },
)
watch(() => [filter.result, filter.limit, period.value, filter.team, filter.cluster], load)
// Кластер другой команды не может остаться выбранным после смены команды
watch(() => filter.team, () => {
  if (filter.cluster && !clusterOptions.value.some(c => c.id === filter.cluster)) filter.cluster = ''
})

function requestFilter(): AuditFilter {
  const ms = PERIODS.find(p => p.id === period.value)?.ms ?? 0
  return { ...filter, since: ms ? new Date(Date.now() - ms).toISOString() : '', until: '' }
}

/** Выгрузка записей по текущему фильтру (до 10 000), файл скачивает браузер */
async function exportLog(format: 'csv' | 'json') {
  exporting.value = true
  try {
    const res = (await callApp('ExportAuditLog', requestFilter(), format)) as { data: string; count: number; truncated: boolean }
    const day = new Date().toISOString().slice(0, 10)
    downloadTextFile(`kubron-audit-${day}.${format}`, res.data, format === 'csv' ? 'text/csv' : 'application/json')
    notifications.notifySuccess(t(res.truncated ? 'audit.export.doneTruncated' : 'audit.export.done', { n: res.count }))
  } catch (e) {
    notifications.notifyError(errorText(e))
  } finally {
    exporting.value = false
  }
}

onMounted(() => {
  load()
  if (capabilities.value.teams) { loadTeams(); loadClusters() }
})
onBeforeUnmount(() => clearTimeout(timer))
</script>

<style scoped>
.audit-view { padding: 24px; text-align: left; max-width: 1300px; }
.audit-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.audit-head h2 { margin: 0; font-size: 22px; color: var(--accent); }
.audit-sub { margin: 4px 0 0; color: var(--text-muted); font-size: 13px; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }

.audit-warn {
  margin: 0 0 16px; padding: 10px 12px; border-radius: 8px; font-size: 13px; line-height: 1.4;
  background: rgba(249, 226, 175, 0.12); border: 1px solid rgba(249, 226, 175, 0.35); color: var(--warn);
}

.audit-filters { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 10px; margin-bottom: 18px; }
.audit-filters .form-input { width: 100%; box-sizing: border-box; padding: 8px 10px; }

.audit-actions { display: flex; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.src { font-size: 11px; }

.audit-empty, .audit-more { color: var(--text-subtle); font-size: 13px; margin: 16px 0; }

.audit-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.audit-table th { text-align: left; padding: 8px 12px; color: var(--text-muted); font-weight: 600; border-bottom: 1px solid var(--surface-0); }
.audit-table td { padding: 9px 12px; border-bottom: 1px solid #232334; vertical-align: top; }
.audit-table tr.denied td { background: rgba(var(--danger-rgb), 0.05); }
.nowrap { white-space: nowrap; }
.muted { color: var(--text-subtle); }
.detail { color: var(--text-muted); font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; word-break: break-word; }

.role-tag { margin-left: 8px; padding: 1px 6px; border-radius: 4px; font-size: 11px; background: var(--surface-0); color: var(--text-muted); }

.badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; white-space: nowrap; }
.badge-ok { background: rgba(166, 227, 161, 0.15); color: var(--ok); }
.badge-failed { background: rgba(249, 226, 175, 0.15); color: var(--warn); }
.badge-denied { background: rgba(var(--danger-rgb), 0.15); color: var(--danger); }
</style>
