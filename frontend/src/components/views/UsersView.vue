<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="users-view">
    <header class="users-head">
      <div>
        <h2>{{ $t('users.title') }}</h2>
        <p class="users-sub">{{ $t('users.subtitle') }}</p>
      </div>
      <button class="btn" type="button" @click="openCreate">{{ $t('users.add') }}</button>
    </header>

    <!-- Роли и их права -->
    <section class="roles-grid">
      <article v-for="r in roles" :key="r.id" class="role-card">
        <h4>{{ roleName(r) }}</h4>
        <ul>
          <li v-for="p in r.permissions" :key="p">{{ permLabel(p) }}</li>
        </ul>
      </article>
    </section>

    <!-- Автовыход по бездействию -->
    <section class="sec-card">
      <div>
        <h4>{{ $t('users.idle.title') }}</h4>
        <p>{{ $t('users.idle.description') }}</p>
      </div>
      <select class="form-input idle-select" :value="auth.idleTimeoutMinutes.value" :disabled="savingIdle"
        :aria-label="$t('users.idle.title')" @change="changeIdle(($event.target as HTMLSelectElement).value)">
        <option v-for="m in idleOptions" :key="m" :value="m">{{ idleLabel(m) }}</option>
      </select>
    </section>

    <!-- Web OIDC public client configuration -->
    <section class="sec-card web-oidc-card">
      <div>
        <h4>{{ $t('users.webOIDC.title') }}</h4>
        <p>{{ $t('users.webOIDC.description') }}</p>
      </div>
      <form class="web-oidc-form" @submit.prevent="saveWebOIDCClient">
        <input v-model="webOIDCClientID" class="form-input" type="text" maxlength="256"
          autocomplete="off" :aria-label="$t('users.webOIDC.clientID')" :placeholder="$t('users.webOIDC.clientID')" />
        <input v-model="webOIDCIssuerURL" class="form-input" type="text" maxlength="512"
          autocomplete="off" :aria-label="$t('users.webOIDC.issuerURL')" :placeholder="$t('users.webOIDC.issuerURL')" />
        <button class="btn" type="submit" :disabled="savingWebOIDC || !webOIDCChanged">
          {{ savingWebOIDC ? $t('users.webOIDC.saving') : $t('users.webOIDC.save') }}
        </button>
        <p v-if="webOIDCClientID.trim() && !webOIDCIssuerURL.trim()" class="form-error web-oidc-hint" role="alert">
          {{ $t('users.webOIDC.issuerRequired') }}
        </p>
      </form>
    </section>

    <p v-if="loadError" class="form-error">{{ loadError }}</p>

    <table v-if="users.length" class="users-table">
      <thead>
        <tr>
          <th>{{ $t('users.columns.login') }}</th>
          <th>{{ $t('users.columns.role') }}</th>
          <th>{{ $t('users.columns.status') }}</th>
          <th>{{ $t('users.columns.lastLogin') }}</th>
          <th class="col-actions">{{ $t('users.columns.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="u in users" :key="u.id" :class="{ disabled: u.disabled }">
          <td>
            <strong>{{ u.username }}</strong>
            <span v-if="isSelf(u)" class="badge badge-you">{{ $t('users.you') }}</span>
          </td>
          <td>
            <select class="form-input role-select" :value="u.role" :disabled="isSelf(u) || busyId === u.id"
              :title="isSelf(u) ? $t('users.cannotChangeOwnRole') : ''"
              @change="changeRole(u, ($event.target as HTMLSelectElement).value)">
              <option v-for="r in roles" :key="r.id" :value="r.id">{{ roleName(r) }}</option>
            </select>
          </td>
          <td>
            <span v-if="u.disabled" class="badge badge-off">{{ $t('users.status.disabled') }}</span>
            <span v-else-if="u.mustChangePassword" class="badge badge-warn" 
            :title="$t('users.status.tempPasswordHint')">
              {{ $t('users.status.tempPassword') }}
            </span>
            <span v-else class="badge badge-ok">{{ $t('users.status.active') }}</span>
          </td>
          <td class="muted">{{ formatDate(u.lastLoginAt) }}</td>
          <td class="col-actions">
            <button class="btn btn-sm btn-secondary" type="button" :disabled="isSelf(u) || busyId === u.id"
              :title="isSelf(u) ? $t('users.ownPasswordHint') : ''" @click="openReset(u)">
              {{ $t('users.resetPassword') }}
            </button>
            <button class="btn btn-sm btn-secondary" type="button" :disabled="isSelf(u) || busyId === u.id"
              @click="toggleDisabled(u)">
              {{ u.disabled ? $t('users.enable') : $t('users.disable') }}
            </button>
            <button class="btn btn-sm btn-danger" type="button" :disabled="isSelf(u) || busyId === u.id"
              @click="remove(u)">
              {{ $t('users.delete') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="!loading" class="muted">{{ $t('users.empty') }}</p>

    <!-- Создание пользователя -->
    <div v-if="dialog === 'create'" class="dlg-overlay" @click.self="closeDialog">
      <form class="dlg" @submit.prevent="submitCreate" autocomplete="off">
        <h3>{{ $t('users.create.title') }}</h3>
        <label class="auth-field">
          <span>{{ $t('users.create.login') }}</span>
          <input ref="firstInput" v-model="form.username" :disabled="saving" required />
        </label>
        <label class="auth-field">
          <span>{{ $t('users.create.tempPassword') }}</span>
          <input v-model="form.password" type="text" autocomplete="off" :disabled="saving" required />
          <small>{{ $t('users.create.passwordHint', { n: MIN_PASSWORD }) }}</small>
        </label>
        <label class="auth-field">
          <span>{{ $t('users.create.role') }}</span>
          <select v-model="form.role" class="form-input" :disabled="saving">
            <option v-for="r in roles" :key="r.id" :value="r.id">{{ roleName(r) }}</option>
          </select>
        </label>
        <p v-if="dialogError" class="auth-error" role="alert">{{ dialogError }}</p>
        <div class="auth-actions">
          <button class="auth-link" type="button" @click="closeDialog">{{ $t('common.cancel') }}</button>
          <button class="auth-submit" type="submit" :disabled="saving || !form.username || !form.password">
            {{ saving ? $t('users.create.submitting') : $t('users.create.submit') }}
          </button>
        </div>
      </form>
    </div>

    <!-- Сброс пароля -->
    <div v-if="dialog === 'reset' && target" class="dlg-overlay" @click.self="closeDialog">
      <form class="dlg" @submit.prevent="submitReset" autocomplete="off">
        <h3>{{ $t('users.reset.title', { name: target.username }) }}</h3>
        <label class="auth-field">
          <span>{{ $t('users.reset.newPassword') }}</span>
          <input ref="firstInput" v-model="form.password" type="text" autocomplete="off" :disabled="saving" required />
          <small>{{ $t('users.reset.passwordHint', { n: MIN_PASSWORD }) }}</small>
        </label>
        <p v-if="dialogError" class="auth-error" role="alert">{{ dialogError }}</p>
        <div class="auth-actions">
          <button class="auth-link" type="button" @click="closeDialog">{{ $t('common.cancel') }}</button>
          <button class="auth-submit" type="submit" :disabled="saving || !form.password">
            {{ saving ? $t('users.reset.submitting') : $t('users.resetPassword') }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { callApp } from '../../resources/wails'
import { askConfirm } from '../../resources/confirm'
import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import { useAuth } from '../../composables/useAuth'
import { formatDateTime } from '../../i18n/format'
import { tl } from '../../i18n'
import { useNotifications } from '../../composables/useNotifications'
import { PERMISSION_LABELS, type RoleInfo, type UserInfo } from '../../types/auth'
import { MIN_PASSWORD } from '../auth/constants'

const { t, te } = useI18n()
const auth = useAuth()
const notifications = useNotifications()

const users = ref<UserInfo[]>([])
const roles = ref<RoleInfo[]>([])
const loading = ref(true)
const loadError = ref('')
const busyId = ref('')

const dialog = ref<'' | 'create' | 'reset'>('')
const target = ref<UserInfo | null>(null)
const saving = ref(false)
const dialogError = ref('')
const form = reactive({ username: '', password: '', role: 'user' })
const firstInput = ref<HTMLInputElement | null>(null)

const IDLE_PRESETS = [0, 5, 10, 15, 30, 60, 120]
const savingIdle = ref(false)
const webOIDCClientID = ref('')
const savedWebOIDCClientID = ref('')
const webOIDCIssuerURL = ref('')
const savedWebOIDCIssuerURL = ref('')
const webOIDCChanged = computed(() =>
  webOIDCClientID.value.trim() !== savedWebOIDCClientID.value || webOIDCIssuerURL.value.trim() !== savedWebOIDCIssuerURL.value)
const savingWebOIDC = ref(false)
// Если на бэкенде задано нестандартное значение, оно тоже должно быть в списке
const idleOptions = computed(() => {
  const cur = auth.idleTimeoutMinutes.value
  return IDLE_PRESETS.includes(cur) ? IDLE_PRESETS : [...IDLE_PRESETS, cur].sort((a, b) => a - b)
})

function idleLabel(m: number): string {
  if (m === 0) return t('users.idle.off')
  if (m === 60) return t('users.idle.oneHour')
  if (m === 120) return t('users.idle.twoHours')
  return t('users.idle.minutes', { n: m })
}

async function changeIdle(value: string) {
  savingIdle.value = true
  try {
    await auth.setIdleTimeout(Number(value))
    notifications.notifySuccess(t('users.idle.saved', { value: idleLabel(auth.idleTimeoutMinutes.value).toLocaleLowerCase() }))
  } catch (e) {
    notifications.notifyError(errorText(e))
  } finally {
    savingIdle.value = false
  }
}

const permLabel = (p: string) => tl(PERMISSION_LABELS[p] ?? p)
/** Роль: подпись из roles.*, иначе подпись бэкенда */
const roleName = (r: RoleInfo) => (te(`roles.${r.id}`) ? t(`roles.${r.id}`) : r.label)
const isSelf = (u: UserInfo) => u.id === auth.user.value?.id

function formatDate(iso: string): string {
  if (!iso) return t('users.neverSignedIn')
  return formatDateTime(iso) || t('common.dash')
}

async function load() {
  loadError.value = ''
  try {
    const [u, r, web] = await Promise.all([
      callApp('ListUsers'), callApp('ListRoles'), callApp('GetWebOIDCClient'),
    ])
    users.value = (u ?? []) as UserInfo[]
    roles.value = (r ?? []) as RoleInfo[]
    const w = (web ?? {}) as { clientId?: string; issuerUrl?: string }
    webOIDCClientID.value = String(w.clientId ?? '')
    webOIDCIssuerURL.value = String(w.issuerUrl ?? '')
    savedWebOIDCClientID.value = webOIDCClientID.value
    savedWebOIDCIssuerURL.value = webOIDCIssuerURL.value
    if (!roles.value.some(x => x.id === form.role) && roles.value.length) form.role = roles.value[roles.value.length - 1].id
  } catch (e) {
    loadError.value = errorText(e)
  } finally {
    loading.value = false
  }
}

async function saveWebOIDCClient() {
  savingWebOIDC.value = true
  try {
    const clientID = webOIDCClientID.value.trim()
    const issuerURL = webOIDCIssuerURL.value.trim()
    await callApp('SetWebOIDCClient', clientID, issuerURL)
    webOIDCClientID.value = clientID
    webOIDCIssuerURL.value = issuerURL
    savedWebOIDCClientID.value = clientID
    savedWebOIDCIssuerURL.value = issuerURL
    notifications.notifySuccess(t('users.webOIDC.saved'))
  } catch (e) {
    notifications.notifyError(errorText(e))
  } finally {
    savingWebOIDC.value = false
  }
}

function replaceUser(updated: UserInfo) {
  users.value = users.value.map(u => (u.id === updated.id ? updated : u))
}

/** Выполнить действие над пользователем с блокировкой строки и уведомлением */
async function act(u: UserInfo, fn: () => Promise<void>, ok: string) {
  busyId.value = u.id
  try {
    await fn()
    notifications.notifySuccess(ok)
  } catch (e) {
    notifications.notifyError(errorText(e))
    await load() // выпавший из синхронизации select вернётся к реальному значению
  } finally {
    busyId.value = ''
  }
}

const changeRole = (u: UserInfo, role: string) =>
  act(u, async () => replaceUser((await callApp('UpdateUserRole', u.id, role)) as UserInfo), t('users.toast.roleChanged', { name: u.username }))

const toggleDisabled = (u: UserInfo) =>
  act(
    u,
    async () => replaceUser((await callApp('SetUserDisabled', u.id, !u.disabled)) as UserInfo),
    u.disabled ? t('users.toast.enabled', { name: u.username }) : t('users.toast.disabled', { name: u.username }),
  )

async function remove(u: UserInfo) {
  if (!(await askConfirm(t('users.confirmDelete', { name: u.username })))) return
  await act(
    u,
    async () => {
      await callApp('DeleteUser', u.id)
      users.value = users.value.filter(x => x.id !== u.id)
    },
    t('users.toast.deleted', { name: u.username }),
  )
}

async function openDialog(kind: 'create' | 'reset') {
  dialogError.value = ''
  form.username = ''
  form.password = ''
  dialog.value = kind
  await nextTick()
  firstInput.value?.focus()
}

const openCreate = () => openDialog('create')

function openReset(u: UserInfo) {
  target.value = u
  return openDialog('reset')
}

function closeDialog() {
  if (saving.value) return
  dialog.value = ''
  target.value = null
  form.password = '' // временный пароль не должен оставаться в памяти формы
}

async function submitDialog(fn: () => Promise<void>) {
  saving.value = true
  dialogError.value = ''
  try {
    await fn()
    saving.value = false
    closeDialog()
  } catch (e) {
    dialogError.value = errorText(e)
  } finally {
    saving.value = false
  }
}

const submitCreate = () =>
  submitDialog(async () => {
    const created = (await callApp('CreateUser', form.username.trim(), form.password, form.role)) as UserInfo
    users.value = [...users.value, created]
    notifications.notifySuccess(t('users.toast.created', { name: created.username }))
  })

const submitReset = () =>
  submitDialog(async () => {
    const tgt = target.value
    if (!tgt) return
    replaceUser((await callApp('ResetUserPassword', tgt.id, form.password)) as UserInfo)
    notifications.notifySuccess(t('users.toast.passwordReset', { name: tgt.username }))
  })

onMounted(load)
</script>


<style scoped>
.users-view { padding: 24px; text-align: left; max-width: 1100px; }
.users-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.users-head h2 { margin: 0; font-size: 22px; color: var(--accent); }
.users-sub { margin: 4px 0 0; color: var(--text-muted); font-size: 13px; }

.roles-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 12px; margin-bottom: 24px; }
.role-card { background: var(--bg-sunken); border: 1px solid var(--surface-0); border-radius: 10px; padding: 12px 16px; }
.role-card h4 { margin: 0 0 8px; color: var(--text); font-size: 14px; }
.role-card ul { margin: 0; padding-left: 18px; color: var(--text-muted); font-size: 12px; line-height: 1.7; }

.sec-card {
  display: flex; align-items: center; justify-content: space-between; gap: 24px; margin-bottom: 24px;
  background: var(--bg-sunken); border: 1px solid var(--surface-0); border-radius: 10px; padding: 14px 16px;
}
.sec-card h4 { margin: 0 0 4px; color: var(--text); font-size: 14px; }
.sec-card p { margin: 0; color: var(--text-muted); font-size: 12px; line-height: 1.5; max-width: 620px; }
.idle-select { width: auto; min-width: 150px; padding: 6px 8px; flex-shrink: 0; }
.web-oidc-form { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 8px; flex-shrink: 0; max-width: 560px; }
.web-oidc-form .form-input { width: min(260px, 100%); }
.web-oidc-hint { flex-basis: 100%; margin: 0; text-align: right; }

.users-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.users-table th { text-align: left; padding: 8px 12px; color: var(--text-muted); font-weight: 600; border-bottom: 1px solid var(--surface-0); }
.users-table td { padding: 10px 12px; border-bottom: 1px solid #232334; vertical-align: middle; }
.users-table tr.disabled td { opacity: 0.55; }
.col-actions { text-align: right; white-space: nowrap; }
.col-actions .btn { margin-left: 6px; }
.btn:disabled { opacity: 0.45; cursor: not-allowed; }
.muted { color: var(--text-subtle); }
.role-select { width: auto; min-width: 150px; padding: 6px 8px; }

.badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
.badge-you { margin-left: 8px; background: rgba(var(--accent-rgb), 0.15); color: var(--accent); }
.badge-ok { background: rgba(166, 227, 161, 0.15); color: var(--ok); }
.badge-off { background: rgba(var(--danger-rgb), 0.15); color: var(--danger); }
.badge-warn { background: rgba(249, 226, 175, 0.15); color: var(--warn); }

.dlg-overlay {
  position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
.dlg {
  width: 100%; max-width: 400px; padding: 24px; display: flex; flex-direction: column; gap: 14px;
  background: var(--bg-sunken); border: 1px solid var(--surface-0); border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5);
}
.dlg h3 { margin: 0; color: var(--accent); font-size: 17px; }
</style>
