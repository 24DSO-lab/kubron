<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="auth.user.value" class="user-menu" data-user-menu>
    <button class="user-chip" type="button" :aria-expanded="open" @click="open = !open">
      <span class="user-avatar">{{ initial }}</span>
      <span class="user-name">{{ auth.user.value.username }}</span>
      <span class="user-role">{{ roleName }}</span>
      <span class="user-caret" :class="{ open }">›</span>
    </button>

    <div v-if="open" class="user-dropdown" role="menu">
      <button class="user-item" role="menuitem" type="button" @click="openChange">{{ $t('auth.menu.changePassword') }}</button>
      <button class="user-item user-item-danger" role="menuitem" type="button" @click="auth.logout()">{{ $t('auth.menu.logout') }}</button>
    </div>

    <ChangePasswordDialog v-if="showChange" @close="showChange = false" />
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuth } from '../../composables/useAuth'
import ChangePasswordDialog from './ChangePasswordDialog.vue'

const { t, te } = useI18n()
const auth = useAuth()
const open = ref(false)
const showChange = ref(false)

const initial = computed(() => (auth.user.value?.username ?? '?').charAt(0).toUpperCase())

/** Подпись роли: перевод по идентификатору, для неизвестной роли — подпись с бэкенда */
const roleName = computed(() => {
  const u = auth.user.value
  if (!u) return ''
  return te(`roles.${u.role}`) ? t(`roles.${u.role}`) : u.roleLabel
})

function openChange() {
  open.value = false
  showChange.value = true
}

function onDocClick(e: MouseEvent) {
  if (!(e.target as Element | null)?.closest?.('[data-user-menu]')) open.value = false
}
onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))
</script>

<style scoped>
.user-menu { position: relative; margin-left: auto; }
.user-chip {
  display: flex; align-items: center; gap: 8px;
  background: var(--bg); border: 1px solid var(--surface-0); border-radius: 999px;
  padding: 4px 12px 4px 4px; color: var(--text); cursor: pointer; font-size: 13px;
}
.user-chip:hover { border-color: var(--surface-1); }
.user-avatar {
  width: 24px; height: 24px; border-radius: 50%;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--accent); color: var(--bg); font-weight: 700; font-size: 12px;
}
.user-name { font-weight: 600; }
.user-role { color: var(--text-muted); font-size: 12px; }
.user-caret { color: var(--text-subtle); transform: rotate(90deg); transition: transform 0.15s; }
.user-caret.open { transform: rotate(-90deg); }

.user-dropdown {
  position: absolute; right: 0; top: calc(100% + 6px); min-width: 180px; z-index: 300;
  background: var(--bg); border: 1px solid var(--surface-0); border-radius: 8px; padding: 4px;
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.45); text-align: left;
}
.user-item {
  display: block; width: 100%; text-align: left; background: none; border: none;
  padding: 8px 10px; border-radius: 6px; color: var(--text); font-size: 13px; cursor: pointer;
}
.user-item:hover { background: var(--surface-0); }
.user-item-danger { color: var(--danger); }
.user-item-danger:hover { background: rgba(var(--danger-rgb), 0.15); }
</style>
