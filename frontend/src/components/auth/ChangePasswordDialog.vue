<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="cp-overlay" @click.self="emit('close')" @keydown.esc="emit('close')">
    <div class="cp-dialog" role="dialog" aria-modal="true" :aria-label="$t('auth.change.title')">
      <h3>{{ $t('auth.change.title') }}</h3>
      <ChangePasswordForm @done="onDone">
        <template #secondary>
          <button class="auth-link" type="button" @click="emit('close')">{{ $t('common.cancel') }}</button>
        </template>
      </ChangePasswordForm>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { useI18n } from 'vue-i18n'
import { useNotifications } from '../../composables/useNotifications'
import ChangePasswordForm from './ChangePasswordForm.vue'

const emit = defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()
const notifications = useNotifications()

function onDone() {
  notifications.notifySuccess(t('auth.change.done'))
  emit('close')
}
</script>

<style scoped>
.cp-overlay {
  position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
.cp-dialog {
  width: 100%; max-width: 380px; padding: 24px; background: var(--bg-sunken);
  border: 1px solid var(--surface-0); border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5);
}
h3 { margin: 0 0 16px; color: var(--accent); font-size: 17px; text-align: left; }
</style>

