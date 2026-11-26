<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hc-view">
    <header class="hc-head">
      <div>
        <h2>{{ $t('helm.catalog.title') }}</h2>
        <p class="hc-sub">{{ $t('helm.catalog.subtitle') }}</p>
      </div>
    </header>

    <form class="hc-filters" @submit.prevent="search">
      <input v-model="query" class="form-input" type="search" :placeholder="$t('helm.catalog.searchPlaceholder')" :aria-label="$t('helm.catalog.searchAria')" />
      <select v-model="repoFilter" class="form-input" :aria-label="$t('helm.catalog.repository')" @change="search">
        <option value="">{{ $t('helm.catalog.allRepos') }}</option>
        <option v-for="r in repoNames" :key="r" :value="r">{{ r }}</option>
      </select>
    </form>

    <p v-if="error" class="form-error">{{ error }}</p>
    <p v-if="loaded && !repoNames.length" class="hc-empty">{{ $t('helm.catalog.noRepos') }}</p>
    <p v-else-if="loaded && !items.length && !error" class="hc-empty">{{ $t('helm.catalog.nothingFound') }}</p>

    <div v-if="items.length" class="hc-layout">
      <div class="hc-list">
        <table class="hc-table">
          <thead><tr><th>{{ $t('helm.catalog.chart') }}</th><th>{{ $t('helm.catalog.repository') }}</th><th>{{ $t('helm.catalog.version') }}</th><th>{{ $t('helm.catalog.app') }}</th><th>{{ $t('helm.catalog.description') }}</th></tr></thead>
          <tbody>
            <tr v-for="c in items" :key="c.repo + '/' + c.name" :class="{ sel: isSelected(c) }" tabindex="0"
              @click="select(c)" @keydown.enter="select(c)">
              <td><strong>{{ c.name }}</strong><span v-if="c.deprecated" class="hc-dep">{{ $t('helm.catalog.deprecated') }}</span></td>
              <td>{{ c.repo }}</td>
              <td class="hc-mono">{{ c.version }}</td>
              <td class="hc-mono">{{ c.appVersion || $t('common.dash') }}</td>
              <td class="hc-desc" :title="c.description">{{ c.description }}</td>
            </tr>
          </tbody>
        </table>
        <p v-if="total > items.length" class="hc-empty">{{ $t('helm.catalog.shown', { count: items.length, total }) }}</p>
      </div>

      <section v-if="selected" class="hc-detail">
        <div class="hc-detail-bar">
          <h3>{{ selected.repo }}/{{ selected.name }}</h3>
          <label class="hc-field">
            {{ $t('helm.catalog.version') }}
            <select v-model="version" class="form-input" :aria-label="$t('helm.catalog.version')">
              <option v-for="v in versions" :key="v.version" :value="v.version">
                {{ v.version }}{{ v.appVersion ? ` (app ${v.appVersion})` : '' }}{{ v.deprecated ? ` · ${$t('helm.catalog.deprecated')}` : '' }}
              </option>
            </select>
          </label>
          <button v-if="canInstall" type="button" class="btn hc-install" :disabled="!pkg || pkgLoading" @click="showInstall = true">
            {{ $t('helm.catalog.install') }}
          </button>
          <div class="hc-seg" role="group" :aria-label="$t('common.actions')">
            <button v-for="t in TABS" :key="t.id" type="button" :class="{ on: tab === t.id }" @click="tab = t.id">{{ t.label }}</button>
          </div>
        </div>

        <p v-if="pkgError" class="form-error">{{ pkgError }}</p>
        <p v-else-if="pkgLoading && !pkg" class="hc-empty">{{ $t('helm.catalog.loadingChart') }}</p>
        <template v-else-if="pkg">
          <!-- README показываем как текст в редакторе: Markdown из внешнего репозитория не рендерим как HTML -->
          <MonacoReadonly v-if="tab === 'readme'" :value="pkg.readme || $t('helm.catalog.noReadme')" language="markdown" />
          <MonacoReadonly v-else-if="tab === 'values'" :value="pkg.values || $t('helm.catalog.noValues')" language="yaml" />
          <dl v-else class="hc-info">
            <dt>{{ $t('helm.catalog.description') }}</dt><dd>{{ pkg.description || $t('common.dash') }}</dd>
            <dt>{{ $t('helm.catalog.chartVer') }}</dt><dd class="hc-mono">{{ pkg.version }}</dd>
            <dt>{{ $t('helm.catalog.appVer') }}</dt><dd class="hc-mono">{{ pkg.appVersion || $t('common.dash') }}</dd>
            <dt>{{ $t('helm.catalog.website') }}</dt><dd class="hc-mono">{{ pkg.home || $t('common.dash') }}</dd>
            <dt>{{ $t('helm.catalog.sources') }}</dt><dd class="hc-mono">{{ (pkg.sources || []).join('\n') || $t('common.dash') }}</dd>
            <dt>{{ $t('helm.catalog.keywords') }}</dt><dd>{{ (pkg.keywords || []).join(', ') || $t('common.dash') }}</dd>
            <dt>{{ $t('helm.catalog.maintainers') }}</dt><dd>{{ (pkg.maintainers || []).join(', ') || $t('common.dash') }}</dd>
          </dl>
        </template>
      </section>
    </div>

    <HelmInstallDialog v-if="showInstall && selected && pkg" :repo="selected.repo" :chart="selected.name" :version="pkg.version"
      :defaults="pkg.values" @close="showInstall = false" />
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import MonacoReadonly from '../resource/MonacoReadonly.vue'
import HelmInstallDialog from './HelmInstallDialog.vue'
import { callApp } from '../../resources/wails'
import { can } from '../../composables/useAuth'
import { Perm } from '../../types/auth'
import { errorText } from '../../composables/errorText'

interface Chart { repo: string; name: string; version: string; appVersion: string; description: string; deprecated: boolean; versions: number }
interface Ver { version: string; appVersion: string; deprecated: boolean }
interface Pkg {
  repo: string; name: string; version: string; appVersion: string; description: string; home: string
  sources: string[] | null; maintainers: string[] | null; keywords: string[] | null; readme: string; values: string
}

const TABS = [
  { id: 'readme', label: 'README' },
  { id: 'values', label: 'Values' },
  { id: 'info', label: 'Info' },
] as const

const canInstall = computed(() => can(Perm.Write))
const showInstall = ref(false)

const query = ref('')
const repoFilter = ref('')
const repoNames = ref<string[]>([])
const items = ref<Chart[]>([])
const total = ref(0)
const loaded = ref(false)
const error = ref('')

const selected = ref<Chart | null>(null)
const versions = ref<Ver[]>([])
const version = ref('')
const tab = ref<'readme' | 'values' | 'info'>('readme')
const pkg = ref<Pkg | null>(null)
const pkgLoading = ref(false)
const pkgError = ref('')

const isSelected = (c: Chart) => selected.value?.repo === c.repo && selected.value?.name === c.name

let searchSeq = 0
async function search() {
  const my = ++searchSeq
  error.value = ''
  try {
    const res = (await callApp('SearchHelmCharts', query.value, repoFilter.value)) as { items: Chart[]; total: number }
    if (my !== searchSeq) return
    items.value = res.items ?? []
    total.value = res.total ?? 0
    if (selected.value && !items.value.some(isSelected)) selected.value = null
  } catch (e: any) {
    if (my === searchSeq) error.value = errorText(e)
  } finally {
    if (my === searchSeq) loaded.value = true
  }
}

let debounce: ReturnType<typeof setTimeout> | undefined
watch(query, () => {
  clearTimeout(debounce)
  debounce = setTimeout(search, 300)
})
onBeforeUnmount(() => clearTimeout(debounce))

let verSeq = 0
async function select(c: Chart) {
  version.value = '' // иначе watch сначала запросит новый чарт со старой версией
  selected.value = c
  const my = ++verSeq
  versions.value = []
  pkg.value = null
  pkgError.value = ''
  try {
    const v = ((await callApp('GetHelmChartVersions', c.repo, c.name)) as Ver[]) ?? []
    if (my !== verSeq) return
    versions.value = v
    version.value = v[0]?.version ?? c.version // версии отсортированы по убыванию
  } catch (e: any) {
    if (my === verSeq) pkgError.value = errorText(e)
  }
}

let pkgSeq = 0
watch([selected, version], async () => {
  const c = selected.value
  if (!c || !version.value) return
  const my = ++pkgSeq
  pkgLoading.value = true
  pkgError.value = ''
  try {
    const p = (await callApp('GetHelmChartPackage', c.repo, c.name, version.value)) as Pkg
    if (my === pkgSeq) pkg.value = p
  } catch (e: any) {
    if (my === pkgSeq) { pkg.value = null; pkgError.value = errorText(e) }
  } finally {
    if (my === pkgSeq) pkgLoading.value = false
  }
})

onMounted(async () => {
  try {
    repoNames.value = ((await callApp('ListHelmRepos')) as { name: string }[]).map(r => r.name)
  } catch (e: any) {
    error.value = errorText(e)
  }
  await search()
})
</script>

<style scoped>
.hc-view { padding: 24px; text-align: left; }
.hc-head h2 { margin: 0; font-size: 22px; color: var(--accent); }
.hc-sub { margin: 4px 0 16px; color: var(--text-muted); font-size: 13px; }
.hc-filters { display: grid; grid-template-columns: 2fr 1fr; gap: 10px; max-width: 700px; margin-bottom: 16px; }
.hc-filters .form-input { width: 100%; box-sizing: border-box; padding: 8px 10px; }
.hc-empty { color: var(--text-subtle); font-size: 13px; margin: 12px 0; }
.hc-layout { display: grid; grid-template-columns: minmax(380px, 1fr) minmax(420px, 1.3fr); gap: 20px; align-items: start; }
.hc-list { max-height: 75vh; overflow: auto; }
.hc-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.hc-table th { position: sticky; top: 0; background: var(--bg); text-align: left; padding: 8px 10px; color: var(--text-muted); border-bottom: 1px solid var(--surface-0); }
.hc-table td { padding: 8px 10px; border-bottom: 1px solid #232334; }
.hc-table tbody tr { cursor: pointer; }
.hc-table tbody tr:hover, .hc-table tbody tr.sel { background: rgba(var(--accent-rgb), 0.08); }
.hc-desc { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-muted); }
.hc-mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
.hc-dep { margin-left: 8px; padding: 1px 6px; border-radius: 4px; font-size: 11px; background: rgba(249, 226, 175, 0.15); color: var(--warn); }
.hc-detail-bar { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; margin-bottom: 10px; }
.hc-detail-bar h3 { margin: 0; font-size: 16px; color: var(--text); }
.hc-field { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-muted); }
.hc-install { margin-left: auto; }
.hc-install + .hc-seg { margin-left: 0; }
.hc-seg { display: inline-flex; border: 1px solid var(--line-strong); border-radius: 4px; overflow: hidden; }
.hc-seg button { padding: 4px 12px; background: transparent; color: var(--gray-400); border: 0; font: inherit; cursor: pointer; }
.hc-seg button.on { background: var(--blue-strong); color: #fff; }
.hc-info { display: grid; grid-template-columns: 170px 1fr; gap: 8px 14px; font-size: 13px; margin: 0; }
.hc-info dt { color: var(--text-subtle); }
.hc-info dd { margin: 0; white-space: pre-line; word-break: break-word; }
</style>
