<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ad">
    <div v-if="configured === null && !statusError" class="rd-empty">{{ $t('common.loading') }}</div>
    <div v-else-if="statusError" class="rd-empty">{{ statusError }}</div>

    <!-- Токен не задан: объяснение и форма -->
    <div v-else-if="!configured" class="ad-setup">
      <i18n-t keypath="argo.diff.intro" tag="p">
        <template #code><code>get</code></template>
      </i18n-t>
      <p class="rd-muted">
        {{ $t('argo.diff.storage') }}
      </p>

      <template v-if="canManage">
        <h4 class="ad-title">{{ $t('argo.diff.getToken') }}</h4>
        <form class="ad-grid" autocomplete="off" @submit.prevent="generateToken">
          <label class="ad-field">
            <span>{{ $t('argo.diff.user') }}</span>
            <input v-model="genUser" class="ad-input" autocomplete="off" spellcheck="false" :aria-label="$t('argo.diff.user')" />
          </label>
          <label class="ad-field">
            <span>{{ $t('argo.diff.password') }}</span>
            <input
              v-model="genPassword"
              class="ad-input"
              type="password"
              autocomplete="new-password"
              spellcheck="false"
              :aria-label="$t('argo.diff.passwordAria')"
            />
          </label>
          <label class="ad-field">
            <span>{{ $t('argo.diff.account') }}</span>
            <input v-model="genAccount" class="ad-input" autocomplete="off" spellcheck="false" :aria-label="$t('argo.diff.accountAria')" />
          </label>
          <label class="ad-field">
            <span>{{ $t('argo.diff.validFor') }}</span>
            <select v-model.number="genDays" class="ad-input" :aria-label="$t('argo.diff.lifetimeAria')">
              <option v-for="d in DAY_OPTIONS" :key="d" :value="d">{{ $t('argo.diff.days', d) }}</option>
            </select>
          </label>
          <div class="ad-actions">
            <button class="rd-btn" type="submit" :disabled="generating || !canGenerate">
              {{ generating ? $t('argo.diff.generating') : $t('argo.diff.generate') }}
            </button>
          </div>
        </form>
        <p class="rd-muted ad-fine">
          {{ $t('argo.diff.passwordNote') }}
        </p>

        <details class="ad-help">
          <summary>{{ $t('argo.diff.helpSummary') }}</summary>
          <i18n-t keypath="argo.diff.helpAccount" tag="p">
            <template #apiKey><code>apiKey</code></template>
            <template #admin><code>admin</code></template>
          </i18n-t>
          <pre class="ad-code">{{ HELP_YAML }}</pre>
          <i18n-t keypath="argo.diff.helpRbac" tag="p">
            <template #perm><code>accounts, update</code></template>
            <template #cmd><code>argocd account generate-token --account kubron</code></template>
          </i18n-t>
        </details>

        <h4 class="ad-title">{{ $t('argo.diff.pasteTitle') }}</h4>
        <form class="ad-form" @submit.prevent="saveToken">
          <input
            v-model="tokenInput"
            class="ad-input"
            type="password"
            autocomplete="off"
            spellcheck="false"
            :placeholder="$t('argo.diff.tokenPlaceholder')"
            :aria-label="$t('argo.diff.tokenPlaceholder')"
          />
          <button class="rd-btn" type="submit" :disabled="saving || !tokenInput.trim()">{{ $t('argo.diff.saveToken') }}</button>
        </form>
      </template>
      <p v-else class="rd-muted">{{ $t('argo.diff.noManage') }}</p>
      <p v-if="error" class="ad-error">{{ error }}</p>
    </div>

    <!-- Токен задан -->
    <template v-else>
      <div class="ad-toolbar">
        <button class="rd-btn" :disabled="loading" @click="load">{{ diff ? $t('argo.diff.reload') : $t('argo.diff.load') }}</button>
        <label v-if="diff" class="ad-check"><input v-model="onlyChanged" type="checkbox" /> {{ $t('argo.diff.onlyChanged') }}</label>
        <span v-if="diff" class="rd-muted">{{ $t('argo.diff.summary', { changed: diff.changed, total: diff.total }) }}</span>
        <button v-if="canManage" class="rd-btn ad-remove" :disabled="loading" @click="removeToken">{{ $t('argo.diff.removeToken') }}</button>
      </div>
      <p v-if="!diff && !loading && !error" class="rd-muted">
        {{ $t('argo.diff.loadHint') }}
      </p>
      <p v-if="loading" class="rd-muted">{{ $t('argo.diff.loading') }}</p>
      <p v-if="error" class="ad-error">{{ error }}</p>

      <template v-if="diff && !loading">
        <div v-if="diff.truncated" class="ad-note">{{ $t('argo.diff.listTruncated') }}</div>
        <div v-if="!visible.length" class="rd-empty">{{ diff.changed ? $t('argo.diff.noMatch') : $t('argo.diff.inSyncAll') }}</div>
        <div v-for="r in visible" :key="keyOf(r)" class="ad-item">
          <button class="ad-head" type="button" :disabled="!r.diff" @click="toggle(keyOf(r))">
            <span class="ad-caret">{{ r.diff ? (open.has(keyOf(r)) ? '▾' : '▸') : '' }}</span>
            <span class="rd-badge" :class="tone(r.state)">{{ label(r.state) }}</span>
            <span class="ad-kind">{{ r.kind }}<span v-if="r.hook" class="rd-muted"> ({{ $t('argo.hook') }})</span></span>
            <span class="ad-name">{{ r.name }}</span>
            <span class="rd-muted ad-ns">{{ r.namespace }}</span>
          </button>
          <pre v-if="open.has(keyOf(r)) && r.diff" class="ad-diff"><span v-for="(l, i) in lines(r.diff)" :key="i" :class="lineClass(l)">{{ l }}</span></pre>
          <div v-if="open.has(keyOf(r)) && r.truncated" class="ad-note">{{ $t('argo.diff.diffTruncated') }}</div>
        </div>
      </template>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { callApp } from '../../../resources/wails'
import { can } from '../../../composables/useAuth'
import { errorText } from '../../../composables/errorText'
import { isArgoTokenMissing } from '../../../i18n/backendErrors'
import { Perm } from '../../../types/auth'
import type { ArgoApplicationInfo, ArgoAPIStatus, ArgoDiff, ArgoDiffState, ArgoResourceDiff } from '../../../types/argocd'

const props = defineProps<{ resource: Partial<ArgoApplicationInfo> }>()
const { t } = useI18n()

const canManage = computed(() => can(Perm.ClusterManage))

const configured = ref<boolean | null>(null)
const statusError = ref('')
const tokenInput = ref('')
const saving = ref(false)

// Получение токена у Argo CD. Пароль живёт только в этом ref и очищается после каждой попытки
const DAY_OPTIONS = [7, 30, 90, 365]
const HELP_YAML = `# argocd-cm
data:
  accounts.kubron: apiKey

# argocd-rbac-cm
data:
  policy.csv: |
    p, role:kubron-readonly, applications, get, */*, allow
    g, kubron, role:kubron-readonly`
const genUser = ref('admin')
const genPassword = ref('')
const genAccount = ref('kubron')
const genDays = ref(30)
const generating = ref(false)
const canGenerate = computed(() => !!genUser.value.trim() && !!genPassword.value && !!genAccount.value.trim())
const loading = ref(false)
const error = ref('')
const diff = ref<ArgoDiff | null>(null)
const onlyChanged = ref(true)
const open = ref<Set<string>>(new Set())

// Бэкенд возвращает ErrArgoTokenNotSet текстом: так вкладка возвращается к форме, если токен удалили снаружи
const rawMessage = (e: unknown) => (e instanceof Error ? e.message : String(e))

async function loadStatus() {
  try {
    const st = (await callApp('GetArgoAPIStatus')) as ArgoAPIStatus
    configured.value = !!st?.configured
    statusError.value = ''
  } catch (e: any) {
    statusError.value = errorText(e)
  }
}
onMounted(loadStatus)

async function saveToken() {
  const token = tokenInput.value
  saving.value = true
  error.value = ''
  try {
    await callApp('SetArgoAPIToken', token)
    configured.value = true
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    tokenInput.value = '' // токен не остаётся в состоянии компонента ни при успехе, ни при ошибке
    saving.value = false
  }
}

async function generateToken() {
  const password = genPassword.value
  generating.value = true
  error.value = ''
  try {
    await callApp('GenerateArgoAPIToken', genAccount.value.trim(), genUser.value.trim(), password, genDays.value)
    configured.value = true
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    genPassword.value = '' // пароль не остаётся в состоянии компонента ни при успехе, ни при ошибке
    generating.value = false
  }
}

async function removeToken() {
  if (!window.confirm(t('argo.diff.removeConfirm'))) return
  error.value = ''
  try {
    await callApp('DeleteArgoAPIToken')
    configured.value = false
    diff.value = null
  } catch (e: any) {
    error.value = errorText(e)
  }
}

let seq = 0
async function load() {
  const ns = props.resource?.namespace
  const name = props.resource?.name
  if (!ns || !name) return
  const my = ++seq
  loading.value = true
  error.value = ''
  try {
    const d = (await callApp('GetArgoApplicationDiff', ns, name)) as ArgoDiff
    if (my !== seq) return
    diff.value = d
    open.value = new Set()
  } catch (e: any) {
    if (my !== seq) return
    if (isArgoTokenMissing(rawMessage(e))) configured.value = false
    else error.value = errorText(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

// Открыто другое приложение: старый diff к нему не относится
watch(() => `${props.resource?.namespace}/${props.resource?.name}`, () => {
  seq++
  diff.value = null
  error.value = ''
  open.value = new Set()
  loading.value = false
})

const visible = computed<ArgoResourceDiff[]>(() =>
  (diff.value?.resources ?? []).filter(r => !onlyChanged.value || r.state !== 'InSync'))

const keyOf = (r: ArgoResourceDiff) => `${r.group}/${r.kind}/${r.namespace}/${r.name}`

function toggle(k: string) {
  const next = new Set(open.value)
  if (!next.delete(k)) next.add(k)
  open.value = next
}

const STATE_KEYS: Record<string, string> = {
  InSync: 'argo.diff.state.inSync',
  Modified: 'argo.diff.state.modified',
  Missing: 'argo.diff.state.missing',
  Extra: 'argo.diff.state.extra',
}
const label = (s: ArgoDiffState) => (STATE_KEYS[s] ? t(STATE_KEYS[s]) : s)
const tone = (s: ArgoDiffState) =>
  ({ InSync: 'rd-ok', Modified: 'rd-warn', Missing: 'rd-warn', Extra: 'rd-err' })[s] ?? 'rd-unk'

const lines = (d: string) => d.split('\n')
const lineClass = (l: string) =>
  l.startsWith('@@') ? 'ad-l-hunk' : l.startsWith('+') ? 'ad-l-add' : l.startsWith('-') ? 'ad-l-del' : 'ad-l-ctx'
</script>

<style scoped>
.ad-setup p { margin: 0 0 10px; line-height: 1.5; font-size: 0.85rem; }
.ad-setup code { font-family: 'Courier New', monospace; background: #1a1d2e; padding: 1px 5px; border-radius: 3px; }
.ad-form { display: flex; gap: 8px; margin: 12px 0; }
.ad-title { margin: 16px 0 6px; font-size: 0.8rem; font-weight: 600; color: var(--gray-400); text-transform: uppercase; letter-spacing: 0.03em; }
.ad-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 10px; margin: 8px 0; }
.ad-field { display: flex; flex-direction: column; gap: 4px; font-size: 0.78rem; color: var(--gray-400); }
.ad-field .ad-input { flex: none; min-width: 0; width: 100%; box-sizing: border-box; }
.ad-actions { display: flex; align-items: flex-end; }
.ad-fine { font-size: 0.78rem; margin: 4px 0 10px; }
.ad-help { margin: 8px 0 12px; font-size: 0.85rem; }
.ad-help summary { cursor: pointer; color: var(--blue); }
.ad-code {
  margin: 8px 0; padding: 8px 10px; background: #12141f; border: 1px solid #2a2f47; border-radius: 4px;
  font-family: 'Courier New', monospace; font-size: 0.8rem; color: #e5e7eb; overflow-x: auto;
}
.ad-input {
  flex: 1; min-width: 200px; padding: 6px 10px; background: var(--panel); color: var(--text-code);
  border: 1px solid var(--line-strong); border-radius: 4px; font: inherit;
}
.ad-error { color: var(--red-soft); font-size: 0.85rem; margin: 8px 0; }
.ad-toolbar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.ad-check { display: flex; align-items: center; gap: 6px; color: var(--text-code); cursor: pointer; }
.ad-remove { margin-left: auto; }
.ad-note { padding: 6px 0; font-size: 0.8rem; color: var(--yellow); }
.ad-item { border-bottom: 1px solid #22263b; }
.ad-head {
  display: flex; align-items: center; gap: 10px; width: 100%; padding: 8px 4px; background: none; border: 0;
  color: inherit; font: inherit; text-align: left; cursor: pointer;
}
.ad-head:disabled { cursor: default; }
.ad-head:not(:disabled):hover { background: #20243a; }
.ad-caret { width: 12px; color: #8b91ad; }
.ad-kind { color: #8b91ad; font-size: 0.8rem; }
.ad-name { font-family: 'Courier New', monospace; font-size: 0.85rem; }
.ad-ns { margin-left: auto; font-size: 0.8rem; }
.ad-diff {
  margin: 0 0 10px; padding: 8px 10px; overflow: auto; max-height: 420px; background: #12141f;
  border: 1px solid #2a2f47; border-radius: 4px; font-family: 'Courier New', monospace; font-size: 0.8rem; line-height: 1.45;
}
.ad-diff span { display: block; white-space: pre; }
.ad-l-add { color: var(--green); background: rgba(74, 222, 128, 0.08); }
.ad-l-del { color: var(--red-soft); background: rgba(248, 113, 113, 0.08); }
.ad-l-hunk { color: var(--blue); }
.ad-l-ctx { color: var(--gray-400); }
</style>
