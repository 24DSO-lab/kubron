<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ms" data-testid="bug-report-mail">
    <p v-if="status && !status.canSave" class="ms-hint" data-testid="bug-report-mail-readonly">{{ $t('bugReport.mail.cannotSave') }}</p>
    <p v-else-if="status?.source === 'environment'" class="ms-hint">{{ $t('bugReport.mail.fromEnvironment') }}</p>
    <div class="ms-grid">
      <label for="ms-host">{{ $t('bugReport.mail.host') }}</label>
      <input id="ms-host" v-model="host" type="text" spellcheck="false" autocomplete="off" :disabled="busy" data-testid="bug-report-mail-host" />
      <label for="ms-port">{{ $t('bugReport.mail.port') }}</label>
      <input id="ms-port" v-model.number="port" type="number" min="1" max="65535" :disabled="busy" data-testid="bug-report-mail-port" />
      <label for="ms-security">{{ $t('bugReport.mail.security') }}</label>
      <select id="ms-security" v-model="security" :disabled="busy" data-testid="bug-report-mail-security" @change="port = defaultPort(security)">
        <option value="starttls">{{ $t('bugReport.mail.starttls') }}</option>
        <option value="tls">{{ $t('bugReport.mail.tls') }}</option>
        <option value="none">{{ $t('bugReport.mail.none') }}</option>
      </select>
      <label for="ms-user">{{ $t('bugReport.mail.username') }}</label>
      <input id="ms-user" v-model="username" type="text" spellcheck="false" autocomplete="off" :disabled="busy" data-testid="bug-report-mail-user" />
      <label for="ms-pass">{{ $t('bugReport.mail.password') }}</label>
      <input id="ms-pass" v-model="password" type="password" spellcheck="false" autocomplete="new-password" :disabled="busy"
        :placeholder="status?.settings.passwordSet ? $t('bugReport.mail.passwordKept') : ''" data-testid="bug-report-mail-password" />
      <label for="ms-from">{{ $t('bugReport.mail.from') }}</label>
      <input id="ms-from" v-model="from" type="email" spellcheck="false" autocomplete="off" :placeholder="username" :disabled="busy" data-testid="bug-report-mail-from" />
      <label for="ms-to">{{ $t('bugReport.mail.to') }}</label>
      <input id="ms-to" v-model="to" type="email" spellcheck="false" autocomplete="off" :disabled="busy" data-testid="bug-report-mail-to" />
    </div>
    <p v-if="error" class="ms-error" role="alert" data-testid="bug-report-mail-error">{{ error }}</p>
    <div class="ms-actions">
      <button type="button" class="btn btn-primary" :disabled="busy || !canSave" data-testid="bug-report-mail-save" @click="save">
        {{ $t('common.save') }}
      </button>
      <button type="button" class="btn" :disabled="busy || status?.source !== 'saved'" data-testid="bug-report-mail-clear" @click="clear">
        {{ $t('bugReport.mail.clear') }}
      </button>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { errorText } from '../../composables/errorText'
import { clearBugReportSettings, getBugReportStatus, saveBugReportSettings } from '../../platform/bugreport'
import type { BugReportStatus, MailSecurity } from '../../types/bugreport'
import { canSaveMailSettings, defaultPort } from './bugReportView'

// Почтовый сервер для отчётов. Пароль уходит только в SaveBugReportSettings, обратно не приходит и очищается
// после сохранения и при закрытии.
const emit = defineEmits<{ (e: 'changed'): void }>()

const status = ref<BugReportStatus | null>(null)
const host = ref('')
const port = ref(587)
const security = ref<MailSecurity>('starttls')
const username = ref('')
const password = ref('')
const from = ref('')
const to = ref('')
const busy = ref(false)
const error = ref('')

const canSave = computed(() =>
  !!status.value?.canSave &&
  canSaveMailSettings(host.value, port.value, username.value, password.value, !!status.value?.settings.passwordSet))

async function load() {
  try {
    error.value = ''
    status.value = await getBugReportStatus()
    const s = status.value.settings
    if (s.host) {
      host.value = s.host; port.value = s.port; security.value = s.security
      username.value = s.username; from.value = s.from; to.value = s.to
    }
  } catch (e) {
    status.value = null
    error.value = errorText(e)
  }
}

async function save() {
  busy.value = true
  try {
    error.value = ''
    await saveBugReportSettings({
      host: host.value.trim(), port: port.value, security: security.value, username: username.value.trim(),
      password: password.value, from: from.value.trim(), to: to.value.trim(),
    })
    password.value = ''
    await load()
    emit('changed')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

async function clear() {
  busy.value = true
  try {
    error.value = ''
    await clearBugReportSettings()
    host.value = username.value = from.value = to.value = password.value = ''
    await load()
    emit('changed')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => { password.value = '' })
</script>

<style scoped>
.ms { margin-top: 8px; padding: 10px; border: 1px solid var(--surface-0); border-radius: 8px; }
.ms-hint { margin: 0 0 8px; color: var(--text-muted); font-size: 12px; }
.ms-grid { display: grid; grid-template-columns: 110px 1fr; gap: 6px 10px; align-items: center; font-size: 13px; color: var(--text); }
.ms-grid input, .ms-grid select {
  width: 100%; box-sizing: border-box; background: var(--bg); color: var(--text); border: 1px solid var(--surface-0);
  border-radius: 6px; padding: 5px 8px; font: inherit;
}
.ms-error { margin: 8px 0 0; color: var(--danger); font-size: 13px; }
.ms-actions { display: flex; gap: 8px; margin-top: 10px; }
</style>
