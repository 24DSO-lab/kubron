<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="teams-view">
    <header class="teams-head">
      <div>
        <h2>{{ $t('teams.title') }}</h2>
        <p class="teams-sub">{{ $t('teams.subtitle') }}</p>
      </div>
      <button v-if="isOrgAdmin" class="btn" type="button" @click="openName('create')">{{ $t('teams.add') }}</button>
    </header>

    <p v-if="loadError" class="form-error">{{ loadError }}</p>

    <table v-if="teams.length" class="teams-table">
      <thead>
        <tr>
          <th>{{ $t('teams.columns.name') }}</th>
          <th>{{ $t('teams.columns.myRole') }}</th>
          <th>{{ $t('teams.columns.clusters') }}</th>
          <th>{{ $t('teams.columns.members') }}</th>
          <th class="col-actions">{{ $t('teams.columns.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="tm in teams" :key="tm.id">
          <td><strong>{{ tm.name }}</strong></td>
          <td>
            <span v-if="tm.myRole" class="badge badge-ok">{{ roleLabel(tm.myRole) }}</span>
            <span v-else class="muted">{{ $t('teams.notMember') }}</span>
          </td>
          <td>{{ tm.clusters }}</td>
          <td>{{ tm.members }}</td>
          <td class="col-actions">
            <button v-if="canManage(tm)" class="btn btn-sm btn-secondary" type="button" @click="openMembers(tm)">
              {{ $t('teams.members') }}
            </button>
            <button v-if="canManage(tm)" class="btn btn-sm btn-secondary" type="button" @click="openName('rename', tm)">
              {{ $t('teams.rename') }}
            </button>
            <button v-if="isOrgAdmin" class="btn btn-sm btn-danger" type="button" :disabled="busyId === tm.id"
              @click="removeTeam(tm)">
              {{ $t('teams.delete') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else-if="!loading && !loadError" class="muted">{{ $t('teams.empty') }}</p>

    <!-- Кластеры по командам: передача другой команде -->
    <section v-if="clusterRows.length" class="teams-clusters">
      <h3>{{ $t('teams.clustersTitle') }}</h3>
      <table class="teams-table">
        <thead>
          <tr>
            <th>{{ $t('teams.columns.cluster') }}</th>
            <th>{{ $t('teams.columns.team') }}</th>
            <th class="col-actions">{{ $t('teams.columns.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in clusterRows" :key="c.id">
            <td><strong>{{ c.displayName }}</strong></td>
            <td>{{ teamName(c.teamId) }}</td>
            <td class="col-actions">
              <select v-if="isTeamAdmin(c.teamId) && moveTargets(c.teamId).length" class="form-input move-select"
                :aria-label="$t('teams.moveTo')" :disabled="busyId === c.id" :value="''"
                @change="moveCluster(c.id, c.displayName, ($event.target as HTMLSelectElement).value)">
                <option value="">{{ $t('teams.moveTo') }}</option>
                <option v-for="tm in moveTargets(c.teamId)" :key="tm.id" :value="tm.id">{{ tm.name }}</option>
              </select>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- Создание и переименование -->
    <div v-if="dialog === 'create' || dialog === 'rename'" class="dlg-overlay" @click.self="closeDialog">
      <form class="dlg" @submit.prevent="submitName" autocomplete="off">
        <h3>{{ dialog === 'create' ? $t('teams.create.title') : $t('teams.rename') }}</h3>
        <label class="auth-field">
          <span>{{ $t('teams.create.name') }}</span>
          <input ref="firstInput" v-model="nameForm" maxlength="64" :disabled="saving" required />
        </label>
        <p v-if="dialogError" class="auth-error" role="alert">{{ dialogError }}</p>
        <div class="auth-actions">
          <button class="auth-link" type="button" @click="closeDialog">{{ $t('common.cancel') }}</button>
          <button class="auth-submit" type="submit" :disabled="saving || !nameForm.trim()">
            {{ dialog === 'create' ? $t('teams.create.submit') : $t('common.save') }}
          </button>
        </div>
      </form>
    </div>

    <!-- Состав команды -->
    <div v-if="dialog === 'members' && target" class="dlg-overlay" @click.self="closeDialog">
      <div class="dlg dlg-wide">
        <h3>{{ $t('teams.membersTitle', { name: target.name }) }}</h3>
        <table v-if="members.length" class="teams-table">
          <tbody>
            <tr v-for="m in members" :key="m.userId">
              <td><strong>{{ m.username }}</strong></td>
              <td>
                <select class="form-input role-select" :value="m.role" :disabled="memberBusy === m.userId"
                  :aria-label="$t('teams.columns.myRole')" @change="setRole(m.userId, ($event.target as HTMLSelectElement).value)">
                  <option v-for="r in ROLES" :key="r" :value="r">{{ roleLabel(r) }}</option>
                </select>
              </td>
              <td class="col-actions">
                <button class="btn btn-sm btn-danger" type="button" :disabled="memberBusy === m.userId"
                  @click="removeMember(m)">{{ $t('teams.removeMember') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-else-if="!membersLoading" class="muted">{{ $t('teams.noMembers') }}</p>

        <div class="add-member">
          <select v-model="newUser" class="form-input" :aria-label="$t('teams.addMember')">
            <option value="">{{ $t('teams.chooseUser') }}</option>
            <option v-for="u in candidates" :key="u.id" :value="u.id">{{ u.username }}</option>
          </select>
          <select v-model="newRole" class="form-input role-select" :aria-label="$t('teams.columns.myRole')">
            <option v-for="r in ROLES" :key="r" :value="r">{{ roleLabel(r) }}</option>
          </select>
          <button class="btn" type="button" :disabled="!newUser || memberBusy === newUser" @click="addMember">
            {{ $t('teams.addMember') }}
          </button>
        </div>
        <p v-if="dialogError" class="auth-error" role="alert">{{ dialogError }}</p>
        <div class="auth-actions">
          <button class="auth-link" type="button" @click="closeDialog">{{ $t('common.close') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { callApp } from '../../platform/api'
import { askConfirm } from '../../resources/confirm'
import { errorText } from '../../composables/errorText'
import { useNotifications } from '../../composables/useNotifications'
import { useTeams } from '../../composables/useTeams'
import { useClusters } from '../../composables/useClusters'
import { can } from '../../composables/useAuth'
import { Perm } from '../../types/auth'
import type { Team, TeamMember, UserBrief } from '../../types'

const { t, te } = useI18n()
const notifications = useNotifications()
const { teams, loading, loadError, loadTeams } = useTeams()
const { clusters, loadClusters } = useClusters()

/** Роли в команде: те же, что и глобальные (admin, operator, user) */
const ROLES = ['admin', 'operator', 'user']
const roleLabel = (r: string) => (te(`roles.${r}`) ? t(`roles.${r}`) : r)

/** Администратор организации: создаёт и удаляет команды, управляет составом любой команды */
const isOrgAdmin = computed(() => can(Perm.UsersManage))
const isTeamAdmin = (teamId?: string) => teams.value.some(tm => tm.id === teamId && tm.myRole === 'admin')
const canManage = (tm: Team) => isOrgAdmin.value || tm.myRole === 'admin'

const teamName = (id?: string) => teams.value.find(tm => tm.id === id)?.name ?? t('common.dash')
// Видны только кластеры своих команд (бэкенд чужие не отдаёт)
const clusterRows = computed(() => clusters.value.filter(c => c.teamId))
const moveTargets = (fromId?: string) => teams.value.filter(tm => tm.id !== fromId && tm.myRole === 'admin')

const busyId = ref('')
const dialog = ref<'' | 'create' | 'rename' | 'members'>('')
const target = ref<Team | null>(null)
const saving = ref(false)
const dialogError = ref('')
const nameForm = ref('')
const firstInput = ref<HTMLInputElement | null>(null)

const members = ref<TeamMember[]>([])
const membersLoading = ref(false)
const memberBusy = ref('')
const users = ref<UserBrief[]>([])
const newUser = ref('')
const newRole = ref('user')
const candidates = computed(() => users.value.filter(u => !members.value.some(m => m.userId === u.id)))

async function reload() {
  await Promise.all([loadTeams(), loadClusters()])
}

async function openName(kind: 'create' | 'rename', tm?: Team) {
  dialogError.value = ''
  target.value = tm ?? null
  nameForm.value = tm?.name ?? ''
  dialog.value = kind
  await nextTick()
  firstInput.value?.focus()
}

function closeDialog() {
  if (saving.value) return
  dialog.value = ''
  target.value = null
}

async function submitName() {
  saving.value = true
  dialogError.value = ''
  try {
    const name = nameForm.value.trim()
    if (dialog.value === 'create') {
      await callApp('CreateTeam', name)
      notifications.notifySuccess(t('teams.toast.created', { name }))
    } else if (target.value) {
      await callApp('RenameTeam', target.value.id, name)
      notifications.notifySuccess(t('teams.toast.renamed', { name }))
    }
    saving.value = false
    dialog.value = ''
    target.value = null
    await reload()
  } catch (e) {
    dialogError.value = errorText(e)
  } finally {
    saving.value = false
  }
}

async function removeTeam(tm: Team) {
  if (!(await askConfirm(t('teams.confirmDelete', { name: tm.name })))) return
  busyId.value = tm.id
  try {
    await callApp('DeleteTeam', tm.id)
    notifications.notifySuccess(t('teams.toast.deleted', { name: tm.name }))
    await reload()
  } catch (e) {
    notifications.notifyError(errorText(e))
  } finally {
    busyId.value = ''
  }
}

async function moveCluster(clusterId: string, clusterName: string, teamId: string) {
  if (!teamId) return
  const to = teamName(teamId)
  if (!(await askConfirm(t('teams.confirmMove', { cluster: clusterName, team: to })))) {
    await reload() // select вернётся к пустому значению
    return
  }
  busyId.value = clusterId
  try {
    await callApp('MoveCluster', clusterId, teamId)
    notifications.notifySuccess(t('teams.toast.moved', { cluster: clusterName, team: to }))
  } catch (e) {
    notifications.notifyError(errorText(e))
  } finally {
    busyId.value = ''
    await reload()
  }
}

async function loadMembers() {
  if (!target.value) return
  membersLoading.value = true
  try {
    members.value = ((await callApp('ListTeamMembers', target.value.id)) ?? []) as TeamMember[]
    users.value = ((await callApp('ListTeamUsers', target.value.id)) ?? []) as UserBrief[]
  } catch (e) {
    dialogError.value = errorText(e)
  } finally {
    membersLoading.value = false
  }
}

async function openMembers(tm: Team) {
  dialogError.value = ''
  target.value = tm
  members.value = []
  newUser.value = ''
  newRole.value = 'user'
  dialog.value = 'members'
  await loadMembers()
}

/** Действие над составом: блокирует строку, обновляет список и счётчики */
async function memberAction(userId: string, fn: () => Promise<void>) {
  memberBusy.value = userId
  dialogError.value = ''
  try {
    await fn()
    await loadMembers()
    await loadTeams()
  } catch (e) {
    dialogError.value = errorText(e)
    await loadMembers() // select вернётся к реальной роли
  } finally {
    memberBusy.value = ''
  }
}

const setRole = (userId: string, role: string) =>
  memberAction(userId, async () => { await callApp('SetTeamMember', target.value!.id, userId, role) })

const removeMember = (m: TeamMember) =>
  memberAction(m.userId, async () => { await callApp('RemoveTeamMember', target.value!.id, m.userId) })

async function addMember() {
  const id = newUser.value
  await memberAction(id, async () => {
    await callApp('SetTeamMember', target.value!.id, id, newRole.value)
    newUser.value = ''
  })
}

onMounted(reload)
</script>

<style scoped>
.teams-view { padding: 24px; text-align: left; max-width: 1100px; }
.teams-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.teams-head h2 { margin: 0; font-size: 22px; color: var(--accent); }
.teams-sub { margin: 4px 0 0; color: var(--text-muted); font-size: 13px; }
.teams-clusters { margin-top: 32px; }
.teams-clusters h3 { margin: 0 0 8px; color: var(--text); font-size: 16px; }

.teams-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.teams-table th { text-align: left; padding: 8px 12px; color: var(--text-muted); font-weight: 600; border-bottom: 1px solid var(--surface-0); }
.teams-table td { padding: 10px 12px; border-bottom: 1px solid #232334; vertical-align: middle; }
.col-actions { text-align: right; white-space: nowrap; }
.col-actions .btn { margin-left: 6px; }
.btn:disabled { opacity: 0.45; cursor: not-allowed; }
.muted { color: var(--text-subtle); }
.role-select, .move-select { width: auto; min-width: 150px; padding: 6px 8px; }

.badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
.badge-ok { background: rgba(166, 227, 161, 0.15); color: var(--ok); }

.add-member { display: flex; gap: 8px; align-items: center; margin-top: 4px; }
.add-member .form-input:first-child { flex: 1; min-width: 0; }

.dlg-overlay {
  position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
.dlg {
  width: 100%; max-width: 400px; padding: 24px; display: flex; flex-direction: column; gap: 14px;
  background: var(--bg-sunken); border: 1px solid var(--surface-0); border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5);
}
.dlg-wide { max-width: 640px; max-height: 80vh; overflow: auto; }
.dlg h3 { margin: 0; color: var(--accent); font-size: 17px; }
</style>
