<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hr-view">
    <header class="hr-head">
      <div>
        <h2>{{ $t('helm.repos.title') }}</h2>
        <p class="hr-sub">{{ $t('helm.repos.subtitle') }}</p>
      </div>
      <div class="hr-head-actions">
        <button class="btn" type="button" :disabled="loading || !repos.length || !!busy" @click="updateAll">
          {{ busy === '*' ? $t('helm.repos.updating') : $t('helm.repos.updateAll') }}
        </button>
        <button v-if="canManage" class="btn" type="button" @click="showForm = !showForm">
          {{ showForm ? $t('helm.repos.hideForm') : '＋ ' + $t('helm.repos.add') }}
        </button>
      </div>
    </header>

    <form v-if="showForm && canManage" class="hr-form" autocomplete="off" @submit.prevent="add">
      <input v-model.trim="form.name" class="form-input" :placeholder="$t('helm.repos.namePlaceholder')" :aria-label="$t('helm.repos.nameAria')" required />
      <input v-model.trim="form.url" class="form-input hr-url" :placeholder="$t('helm.repos.urlPlaceholder')" :aria-label="$t('helm.repos.urlAria')" required />
      <input v-model.trim="form.username" class="form-input" :placeholder="$t('helm.repos.userPlaceholder')" :aria-label="$t('helm.repos.userAria')"
        autocomplete="off" />
      <input v-model="form.password" class="form-input" type="password" :placeholder="$t('helm.repos.passPlaceholder')" :aria-label="$t('helm.repos.passAria')"
        autocomplete="new-password" />
      <input v-if="isOci" v-model.trim="form.charts" class="form-input hr-charts" :placeholder="$t('helm.repos.ociChartsPlaceholder')" :aria-label="$t('helm.repos.ociChartsAria')" />
      <label v-if="!isOci" class="hr-check">
        <input v-model="form.insecureTLS" type="checkbox" /> {{ $t('helm.repos.noTLS') }}
      </label>
      <p v-if="isOci" class="hr-note">
        {{ $t('helm.repos.ociNote') }}
      </p>
      <p class="hr-note">{{ $t('helm.repos.passNote') }}</p>
      <div class="hr-form-actions">
        <button class="btn" type="submit" :disabled="adding">{{ adding ? $t('helm.repos.checking') : $t('helm.repos.adding') }}</button>
      </div>
    </form>

    <p v-if="error" class="form-error">{{ error }}</p>
    <p v-if="loading && !loaded" class="hr-empty">{{ $t('common.loading') }}</p>
    <p v-else-if="loaded && !repos.length" class="hr-empty">{{ $t('helm.repos.noRepos') }}</p>

    <table v-if="repos.length" class="hr-table">
      <thead>
        <tr><th>{{ $t('helm.repos.title') }}</th><th>URL</th><th>{{ $t('helm.repos.charts') }}</th><th>{{ $t('helm.repos.updated') }}</th><th>{{ $t('helm.repos.state') }}</th><th></th></tr>
      </thead>
      <tbody>
        <tr v-for="r in repos" :key="r.name">
          <td><strong>{{ r.name }}</strong></td>
          <td class="hr-mono" :title="r.url">
            {{ r.url }}<span v-if="r.kind === 'oci'" class="hr-tag">{{ $t('helm.repos.tagOci') }}</span><span v-if="r.username" class="hr-tag">{{ $t('helm.repos.tagAuth') }}</span><span v-if="r.insecureTLS" class="hr-tag warn">{{ $t('helm.repos.tagNoTls') }}</span>
            <div v-if="r.kind === 'oci'" class="hr-chips">
              <span v-for="c in r.chartNames" :key="c" class="hr-chip">
                {{ c }}<button v-if="canManage" type="button" class="hr-chip-x" :title="$t('helm.repos.removeChartLabel', { c })" :disabled="!!busy" @click="removeChart(r.name, c)">✕</button>
              </span>
              <form v-if="canManage" class="hr-chip-add" @submit.prevent="addChart(r.name)">
                <input v-model.trim="newChart[r.name]" class="form-input" :placeholder="$t('helm.repos.addChartPlaceholder')" :aria-label="$t('helm.repos.addChartAria')" :disabled="!!busy" />
              </form>
            </div>
          </td>
          <td>{{ r.updated ? r.charts : $t('common.dash') }}</td>
          <td class="hr-muted" :title="r.updated">{{ formatTime(r.updated) }}</td>
          <td><span v-if="r.error" class="hr-bad" :title="r.error">{{ r.error }}</span><span v-else class="hr-ok">{{ $t('helm.repos.ok') }}</span></td>
          <td class="hr-actions">
            <button v-if="canManage" class="btn" type="button" :disabled="!!busy" @click="update(r.name)">{{ busy === r.name ? '…' : $t('helm.repos.update') }}</button>
            <button v-if="canManage" class="btn hr-danger" type="button" :disabled="!!busy" @click="remove(r.name)">{{ $t('helm.repos.remove') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { callApp } from '../../resources/wails'
import { askConfirm } from '../../resources/confirm'
import { can } from '../../composables/useAuth'
import { useNotifications } from '../../composables/useNotifications'
import { useI18n } from 'vue-i18n'
import { Perm } from '../../types/auth'
import { errorText } from '../../composables/errorText'
import { formatDateTime } from '../../i18n/format'

const { t } = useI18n()

interface RepoInfo {
  name: string; url: string; username: string; insecureTLS: boolean; charts: number; updated: string; error: string
  kind: 'http' | 'oci'; chartNames: string[] | null
}
interface UpdateResult { name: string; charts: number; error: string }

const notifications = useNotifications()
const canManage = computed(() => can(Perm.ClusterManage))

const repos = ref<RepoInfo[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const busy = ref('') // имя репозитория или '*' для «обновить все»
const adding = ref(false)
const showForm = ref(false)
const form = reactive({ name: '', url: '', username: '', password: '', insecureTLS: false, charts: '' })
const newChart = reactive<Record<string, string>>({})
const isOci = computed(() => form.url.trim().toLowerCase().startsWith('oci://'))

function formatTime(iso: string) {
  if (!iso) return t('common.dash')
  return formatDateTime(iso) || iso
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    repos.value = ((await callApp('ListHelmRepos')) as RepoInfo[]) ?? []
    loaded.value = true
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

async function add() {
  adding.value = true
  error.value = ''
  try {
    if (isOci.value) {
      const charts = form.charts.split(/[\s,]+/).filter(Boolean)
      await callApp('AddHelmOCIRepo', form.name, form.url, charts, form.username, form.password)
    } else {
      await callApp('AddHelmRepo', form.name, form.url, form.username, form.password, form.insecureTLS)
    }
    notifications.notifySuccess(t('helm.repos.added', { name: form.name }))
    Object.assign(form, { name: '', url: '', username: '', password: '', insecureTLS: false, charts: '' })
    showForm.value = false
    await load()
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    form.password = '' // пароль не держим в памяти формы дольше необходимого
    adding.value = false
  }
}

function report(results: UpdateResult[]) {
  const failed = results.filter(r => r.error)
  if (failed.length) {
    notifications.notifyError(t('helm.repos.notUpdated', { list: failed.map(r => r.name + ' (' + r.error + ')').join('; ') }))
  } else {
    notifications.notifySuccess(results.length === 1 ? t('helm.repos.updatedOne', { name: results[0].name }) : t('helm.repos.updatedMany', { count: results.length }))
  }
}

async function runUpdate(name: string, key: string) {
  busy.value = key
  try {
    report(((await callApp('UpdateHelmRepos', name)) as UpdateResult[]) ?? [])
  } catch (e: any) {
    notifications.notifyError(t('helm.repos.updateError', { message: errorText(e) }))
  } finally {
    busy.value = ''
    await load()
  }
}
const update = (name: string) => runUpdate(name, name)
const updateAll = () => runUpdate('', '*')

async function addChart(repo: string) {
  const chart = (newChart[repo] || '').trim()
  if (!chart) return
  busy.value = repo
  try {
    await callApp('AddHelmOCIChart', repo, chart)
    newChart[repo] = ''
  } catch (e: any) {
    notifications.notifyError(t('helm.repos.addChartError', { message: errorText(e) }))
  } finally {
    busy.value = ''
    await load()
  }
}

async function removeChart(repo: string, chart: string) {
  busy.value = repo
  try {
    await callApp('RemoveHelmOCIChart', repo, chart)
  } catch (e: any) {
    notifications.notifyError(t('helm.repos.removeChartError', { message: errorText(e) }))
  } finally {
    busy.value = ''
    await load()
  }
}

async function remove(name: string) {
  if (!(await askConfirm(t('helm.repos.confirmRemove', { name })))) return
  busy.value = name
  try {
    await callApp('RemoveHelmRepo', name)
    notifications.notifySuccess(t('helm.repos.removed', { name }))
  } catch (e: any) {
    notifications.notifyError(t('helm.repos.removeError', { message: errorText(e) }))
  } finally {
    busy.value = ''
    await load()
  }
}

onMounted(load)
</script>

<style scoped>
.hr-view { padding: 24px; text-align: left; max-width: 1200px; }
.hr-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.hr-head h2 { margin: 0; font-size: 22px; color: var(--accent); }
.hr-sub { margin: 4px 0 0; color: var(--text-muted); font-size: 13px; }
.hr-head-actions { display: flex; gap: 8px; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }

.hr-form { display: grid; grid-template-columns: 1fr 2fr 1fr 1fr; gap: 10px; margin-bottom: 20px; padding: 14px; border: 1px solid var(--surface-0); border-radius: 8px; }
.hr-form .form-input { width: 100%; box-sizing: border-box; padding: 8px 10px; }
.hr-check { grid-column: 1 / -1; font-size: 13px; color: var(--text-muted); display: flex; gap: 8px; align-items: center; }
.hr-note { grid-column: 1 / -1; margin: 0; font-size: 12px; color: var(--text-subtle); }
.hr-form-actions { grid-column: 1 / -1; }
.hr-charts { grid-column: 1 / -1; }
.hr-chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; align-items: center; }
.hr-chip { display: inline-flex; align-items: center; gap: 4px; padding: 1px 8px; border-radius: 10px; background: var(--surface-0); color: var(--text); font-size: 12px; }
.hr-chip-x { background: none; border: 0; color: var(--text-muted); cursor: pointer; font-size: 11px; padding: 0; }
.hr-chip-x:hover { color: var(--danger); }
.hr-chip-add .form-input { width: 110px; padding: 2px 8px; font-size: 12px; }

.hr-empty { color: var(--text-subtle); font-size: 13px; margin: 16px 0; }
.hr-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.hr-table th { text-align: left; padding: 8px 12px; color: var(--text-muted); font-weight: 600; border-bottom: 1px solid var(--surface-0); }
.hr-table td { padding: 9px 12px; border-bottom: 1px solid #232334; vertical-align: middle; }
.hr-mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; word-break: break-all; }
.hr-muted { color: var(--text-subtle); white-space: nowrap; }
.hr-ok { color: var(--ok); }
.hr-bad { color: var(--danger); }
.hr-tag { margin-left: 8px; padding: 1px 6px; border-radius: 4px; font-size: 11px; background: var(--surface-0); color: var(--text-muted); }
.hr-tag.warn { color: var(--warn); }
.hr-actions { display: flex; gap: 6px; justify-content: flex-end; white-space: nowrap; }
.hr-danger { color: var(--danger); }
</style>
