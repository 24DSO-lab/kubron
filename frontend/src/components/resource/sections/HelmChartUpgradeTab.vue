<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hc">
    <div v-if="!allowed" class="rd-empty">{{ $t('helm.chartUpgrade.noPerm') }}</div>
    <div v-else-if="error && !loaded" class="rd-empty">{{ error }}</div>
    <div v-else-if="!loaded" class="rd-empty">{{ $t('common.loading') }}</div>
    <div v-else-if="!sources?.sources.length" class="rd-empty">
      {{ $t('helm.chartUpgrade.noSources', { chart: sources?.chart }) }}
    </div>
    <template v-else>
      <p class="rd-muted hc-hint">{{ $t('helm.chartUpgrade.hint') }}</p>

      <div class="hc-form">
        <div class="hc-installed">
          <span class="rd-muted">{{ $t('helm.chartUpgrade.installed') }}</span>
          <strong>{{ sources.chart }} {{ sources.current }}</strong>
        </div>
        <label class="hc-field">{{ $t('helm.chartUpgrade.repository') }}
          <select v-model="repo" class="form-input" :disabled="step !== 'edit' || busy">
            <option v-for="s in sources.sources" :key="s.repo" :value="s.repo">{{ s.repo }}</option>
          </select>
        </label>
        <label class="hc-field">{{ $t('helm.chartUpgrade.version') }}
          <select v-model="version" class="form-input" :disabled="step !== 'edit' || busy">
            <option value="" disabled>{{ $t('helm.chartUpgrade.pickVersion') }}</option>
            <option v-for="v in visibleVersions" :key="v.version" :value="v.version">{{ versionLabel(v) }}</option>
          </select>
        </label>
        <label class="hc-check">
          <input v-model="showPre" type="checkbox" :disabled="step !== 'edit' || busy" />
          {{ $t('helm.chartUpgrade.showPrereleases') }}
        </label>
      </div>

      <p v-if="source && !source.owns" class="hc-warn">{{ $t('helm.chartUpgrade.notOwner', { version: sources.current }) }}</p>
      <p v-else-if="source && !hasNewer" class="rd-muted hc-hint">{{ $t('helm.chartUpgrade.noNewer') }}</p>
      <p v-if="selected?.relation === 'older'" class="hc-warn">{{ $t('helm.chartUpgrade.downgrade') }}</p>
      <p v-if="selected?.deprecated" class="hc-warn">{{ $t('helm.chartUpgrade.deprecated') }}</p>

      <template v-if="step === 'edit'">
        <p class="rd-muted hc-hint">{{ $t('helm.chartUpgrade.valuesHint') }}</p>
        <MonacoDiff :original="original" v-model:modified="edited" editable />
      </template>
      <template v-else>
        <div class="hc-summary">
          <strong>{{ sources.chart }} {{ result?.currentVersion }} → {{ result?.newVersion }}</strong>
          <span v-if="result?.currentAppVersion !== result?.newAppVersion" class="rd-muted">
            {{ $t('helm.chartUpgrade.appVersion', { from: result?.currentAppVersion || '—', to: result?.newAppVersion || '—' }) }}
          </span>
        </div>
        <div class="rd-muted hc-hint">{{ $t('helm.chartUpgrade.manifestChanges') }}</div>
        <MonacoDiff :original="result?.currentManifest ?? ''" :modified="result?.newManifest ?? ''" />
      </template>

      <div v-if="stale" class="hc-stale">
        {{ $t('helm.chartUpgrade.stale', { rev: props.resource?.revision, base: baseRevision }) }}
        <button type="button" class="rd-btn" :disabled="busy" @click="load">{{ $t('helm.chartUpgrade.reload') }}</button>
      </div>

      <p v-if="error" class="hc-error">{{ error }}</p>

      <div class="hc-actions">
        <template v-if="step === 'edit'">
          <button class="rd-btn" :disabled="!canPreview" @click="preview">{{ busy ? $t('helm.chartUpgrade.calculating') : $t('helm.chartUpgrade.previewBtn') }}</button>
          <button class="rd-btn" :disabled="!dirty || busy" @click="edited = original">{{ $t('helm.chartUpgrade.reset') }}</button>
        </template>
        <template v-else>
          <button class="rd-btn" :disabled="busy" @click="back">{{ $t('helm.chartUpgrade.back') }}</button>
          <button class="rd-btn hc-apply" :disabled="busy" @click="apply">{{ busy ? $t('helm.chartUpgrade.applying') : $t('helm.chartUpgrade.applyBtn') }}</button>
        </template>
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import MonacoDiff from '../MonacoDiff.vue'
import { callApp } from '../../../resources/wails'
import { runHelmOp } from '../../../composables/useHelmOps'
import { askConfirm } from '../../../resources/confirm'
import { withFluxWarning } from './HelmFluxGuard'
import { reloadActive } from '../../../resources/store'
import { itemName, itemNs } from '../../../resources/utils'
import { can } from '../../../composables/useAuth'
import { useNotifications } from '../../../composables/useNotifications'
import { Perm } from '../../../types/auth'
import { errorText } from '../../../composables/errorText'

interface UpgradeVersion {
  version: string
  appVersion: string
  deprecated: boolean
  prerelease: boolean
  /** newer | current | older; пусто, если установленная версия не semver */
  relation: string
}
interface UpgradeSource { repo: string; owns: boolean; versions: UpgradeVersion[] }
interface UpgradeSources { chart: string; current: string; sources: UpgradeSource[] }
interface UpgradePreview {
  currentVersion: string
  newVersion: string
  currentAppVersion: string
  newAppVersion: string
  currentManifest: string
  newManifest: string
}

const { t } = useI18n()
const props = defineProps<{ resource: any }>()
const notifications = useNotifications()

const ns = computed(() => itemNs(props.resource))
const name = computed(() => itemName(props.resource))
const allowed = computed(() => can(Perm.Write) && can(Perm.SecretsRead))

const step = ref<'edit' | 'preview'>('edit')
const loaded = ref(false)
const busy = ref(false)
const error = ref('')
const sources = ref<UpgradeSources | null>(null)
const repo = ref('')
const version = ref('')
const showPre = ref(false)
const original = ref('')
const edited = ref('')
const result = ref<UpgradePreview | null>(null)
const baseRevision = ref<number | undefined>(undefined) // ревизия, на которой основан экран
const stale = ref(false)

const dirty = computed(() => edited.value !== original.value)
const source = computed(() => sources.value?.sources.find(s => s.repo === repo.value))
const installedIsPre = computed(() => (sources.value?.current ?? '').includes('-'))

/** Пререлизы скрыты, пока их не включили; выбранная и текущая версии остаются видимыми всегда */
const visibleVersions = computed(() =>
  (source.value?.versions ?? []).filter(v =>
    showPre.value || installedIsPre.value || !v.prerelease || v.version === version.value || v.relation === 'current'))
const selected = computed(() => source.value?.versions.find(v => v.version === version.value))
const hasNewer = computed(() => visibleVersions.value.some(v => v.relation === 'newer'))
const canPreview = computed(() => !!version.value && !!selected.value && selected.value.relation !== 'current' && !busy.value)

function versionLabel(v: UpgradeVersion): string {
  let s = v.appVersion ? `${v.version} · ${t('helm.chartUpgrade.app')} ${v.appVersion}` : v.version
  const tags: string[] = []
  if (v.relation === 'current') tags.push(t('helm.chartUpgrade.tagCurrent'))
  if (v.prerelease) tags.push(t('helm.chartUpgrade.tagPrerelease'))
  if (v.deprecated) tags.push(t('helm.chartUpgrade.tagDeprecated'))
  if (tags.length) s += ` (${tags.join(', ')})`
  return s
}

/** Версия по умолчанию: та, о которой сообщает индикатор обновления, иначе самая новая стабильная; иначе выбор за пользователем */
function defaultVersion(s: UpgradeSource | undefined): string {
  if (!s) return ''
  const hinted = props.resource?.latestVersion
  if (hinted && s.versions.some(v => v.version === hinted && v.relation !== 'current')) return hinted
  return s.versions.find(v => v.relation === 'newer' && (installedIsPre.value || !v.prerelease))?.version ?? ''
}

// Смена репозитория сбрасывает версию: версии у репозиториев свои
watch(repo, () => {
  version.value = defaultVersion(source.value)
})

let seq = 0
async function load() {
  const my = ++seq
  loaded.value = false
  error.value = ''
  step.value = 'edit'
  stale.value = false
  result.value = null
  if (!allowed.value) return
  try {
    // revision 0 — последняя; user-values переносятся на новую версию чарта
    const [src, content] = await Promise.all([
      callApp('GetHelmUpgradeSources', ns.value, name.value) as Promise<UpgradeSources>,
      callApp('GetHelmReleaseContent', ns.value, name.value, 0),
    ])
    if (my !== seq) return
    sources.value = src
    original.value = content.values ?? ''
    edited.value = original.value
    baseRevision.value = props.resource?.revision
    const preferred = src.sources.find((s: UpgradeSource) => s.repo === props.resource?.latestRepo) ?? src.sources[0]
    const repoChanged = repo.value !== preferred?.repo
    repo.value = preferred?.repo ?? ''
    if (!repoChanged) version.value = defaultVersion(preferred) // watch(repo) не сработает: значение то же
    loaded.value = true
  } catch (e: any) {
    if (my === seq) error.value = errorText(e)
  }
}
// Релиз сменился: всё перечитываем
watch(() => [ns.value, name.value], load, { immediate: true })
// Ревизия изменилась (live-обновление): без правок и предпросмотра тихо перечитываем, иначе предупреждаем и ничего не затираем
watch(() => props.resource?.revision, rev => {
  if (!loaded.value || rev === baseRevision.value) return
  if (dirty.value || step.value === 'preview' || busy.value) stale.value = true
  else load()
})

function request(opId = '') {
  return { namespace: ns.value, name: name.value, repo: repo.value, version: version.value, values: edited.value, opId }
}

async function preview() {
  busy.value = true
  error.value = ''
  try {
    result.value = (await callApp('PreviewHelmChartUpgrade', request())) as UpgradePreview
    step.value = 'preview'
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

function back() {
  step.value = 'edit'
  error.value = ''
}

async function apply() {
  const from = result.value?.currentVersion ?? sources.value?.current ?? ''
  const message = withFluxWarning('upgrade', props.resource, name.value, t('helm.chartUpgrade.confirm', { name: name.value, from, to: version.value }))
  if (!(await askConfirm(message))) return
  busy.value = true
  error.value = ''
  const to = version.value
  try {
    const rev = await runHelmOp({ op: 'upgrade', namespace: ns.value, release: name.value, cancelable: true }, id =>
      callApp('UpgradeHelmChart', request(id)) as Promise<number>)
    notifications.notifySuccess(t('helm.chartUpgrade.upgraded', { name: name.value, version: to, rev }))
    await reloadActive()
    await load()
    baseRevision.value = rev // собственная ревизия не считается чужим изменением
    stale.value = false
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.hc-hint { font-size: 0.85rem; margin: 0 0 8px; }
.hc-form { display: grid; grid-template-columns: 1fr 1fr; gap: 10px 16px; margin-bottom: 10px; }
.hc-installed { grid-column: 1 / -1; display: flex; gap: 8px; align-items: baseline; font-size: 0.9rem; }
.hc-field { display: flex; flex-direction: column; gap: 4px; font-size: 0.85rem; color: #a6adc8; min-width: 0; }
.hc-field .form-input { padding: 6px 8px; }
.hc-check { grid-column: 1 / -1; display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: #cdd6f4; }
.hc-warn { color: #f9e2af; font-size: 0.85rem; margin: 0 0 8px; }
.hc-summary { display: flex; gap: 12px; align-items: baseline; flex-wrap: wrap; margin-bottom: 6px; }
.hc-stale { padding: 8px 10px; border: 1px solid #f9e2af; border-radius: 6px; color: #f9e2af; font-size: 0.85rem; display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-top: 8px; }
.hc-actions { display: flex; gap: 8px; margin-top: 10px; }
.hc-actions .rd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.hc-error { color: #f87171; font-size: 0.85rem; margin: 8px 0 0; white-space: pre-wrap; }
</style>
