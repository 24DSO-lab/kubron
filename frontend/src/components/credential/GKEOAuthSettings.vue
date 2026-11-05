<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="settings-card" data-testid="gke-oauth">
    <div class="settings-card-header">
      <div class="settings-card-header-row">
        <div class="settings-card-header-left">
          <div>
            <h2 class="settings-card-heading">{{ $t('credential.gkeOAuth.title') }}</h2>
            <p class="settings-card-description">{{ $t('credential.gkeOAuth.description') }}</p>
          </div>
        </div>
      </div>
    </div>
    <div class="settings-card-body">
      <p v-if="error" class="go-error" role="alert" data-testid="gke-oauth-error">{{ error }}</p>
      <p class="go-state" role="status" data-testid="gke-oauth-state">{{ $t(`credential.gkeOAuth.state.${stateKey}`) }}</p>
      <div class="go-row">
        <label for="gke-oauth-id">{{ $t('credential.gkeOAuth.clientId') }}</label>
        <input id="gke-oauth-id" v-model="clientId" type="text" spellcheck="false" autocomplete="off" autocapitalize="off"
          :disabled="busy" data-testid="gke-oauth-client-id" />
      </div>
      <div class="go-row">
        <label for="gke-oauth-secret">{{ $t('credential.gkeOAuth.clientSecret') }}</label>
        <input id="gke-oauth-secret" v-model="clientSecret" type="password" spellcheck="false" autocomplete="off"
          :placeholder="status?.secretSet ? $t('credential.gkeOAuth.secretKept') : ''" :disabled="busy"
          data-testid="gke-oauth-client-secret" />
      </div>
      <p class="go-hint">{{ $t('credential.gkeOAuth.hint') }}</p>
      <div class="go-actions">
        <button type="button" class="btn btn-primary" :disabled="busy || !canSave" data-testid="gke-oauth-save" @click="save">
          {{ $t('credential.gkeOAuth.save') }}
        </button>
        <button type="button" class="btn" :disabled="busy || !status?.configured" data-testid="gke-oauth-clear" @click="clear">
          {{ $t('credential.gkeOAuth.clear') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { errorText } from '../../composables/errorText'
import { clearGKEOAuth, getGKEOAuth, saveGKEOAuth } from '../../platform/credential'
import type { GKEOAuthStatus } from '../../types/credential'
import { canSaveGKEOAuth, gkeOAuthStateKey } from './gkeOAuthView'

// Настройка OAuth-клиента Google для входа в GKE (десктоп). Секрет вводится сюда, уходит только в SaveGKEOAuth,
// не показывается обратно, не пишется в журнал и очищается после сохранения и при закрытии.
const status = ref<GKEOAuthStatus | null>(null)
const clientId = ref('')
const clientSecret = ref('')
const busy = ref(false)
const error = ref('')

const stateKey = computed(() => gkeOAuthStateKey(status.value))
const canSave = computed(() => canSaveGKEOAuth(clientId.value, clientSecret.value))

async function load() {
  try {
    error.value = ''
    status.value = await getGKEOAuth()
    clientId.value = status.value.clientId
  } catch (e) {
    status.value = null
    error.value = errorText(e)
  }
}

async function save() {
  busy.value = true
  try {
    error.value = ''
    await saveGKEOAuth(clientId.value.trim(), clientSecret.value)
    clientSecret.value = ''
    await load()
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
    await clearGKEOAuth()
    clientId.value = ''
    clientSecret.value = ''
    await load()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => { clientSecret.value = '' })
</script>

<style scoped>
.go-row { display: flex; flex-direction: column; gap: 0.25rem; margin-bottom: 0.75rem; }
.go-actions { display: flex; gap: 0.5rem; }
.go-hint { opacity: 0.8; font-size: 0.85em; margin: 0 0 0.75rem; }
.go-error { color: var(--color-danger, #ef4444); }
</style>
