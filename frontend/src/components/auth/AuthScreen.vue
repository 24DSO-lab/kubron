<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="auth-screen">
    <div class="auth-lang"><LanguageSwitcher /></div>
    <div class="auth-card">
      <img src="../../assets/images/logo.svg" alt="Kubron" class="auth-logo" />

      <!-- Загрузка -->
      <p v-if="mode === 'loading'" class="auth-hint">{{ $t('auth.loading') }}</p>

      <!-- Критическая ошибка хранилища пользователей: входить нельзя -->
      <template v-else-if="mode === 'fatal'">
        <h1>{{ $t('auth.fatal.title') }}</h1>
        <p class="auth-error" role="alert">{{ auth.fatalError.value }}</p>
        <i18n-t keypath="auth.fatal.hint" tag="p" class="auth-hint">
          <template #file><code>~/.kubron/users.json</code></template>
        </i18n-t>
      </template>

      <!-- Первый запуск -->
      <template v-else-if="mode === 'setup'">
        <h1>{{ $t('auth.setup.title') }}</h1>
        <p class="auth-hint">{{ $t('auth.setup.hint') }}</p>
        <form class="auth-form" @submit.prevent="submitSetup" autocomplete="off">
          <label v-if="auth.setupTokenRequired.value" class="auth-field">
            <span>{{ $t('auth.setup.token') }}</span>
            <input v-model="setupToken" type="password" autocomplete="off" spellcheck="false" :disabled="busy" required />
            <small>{{ $t('auth.setup.tokenHint') }}</small>
          </label>
          <label class="auth-field">
            <span>{{ $t('auth.fields.username') }}</span>
            <input ref="firstInput" v-model="username" autocomplete="username" :disabled="busy" required />
            <small>{{ $t('auth.setup.usernameHint') }}</small>
          </label>
          <label class="auth-field">
            <span>{{ $t('auth.fields.password') }}</span>
            <input v-model="password" type="password" autocomplete="new-password" :disabled="busy" required />
            <small>{{ $t('auth.fields.minLength', { min: MIN_PASSWORD }) }}</small>
          </label>
          <label class="auth-field">
            <span>{{ $t('auth.fields.confirmPassword') }}</span>
            <input v-model="confirm" type="password" autocomplete="new-password" :disabled="busy" required />
          </label>
          <p v-if="error" class="auth-error" role="alert">{{ error }}</p>
          <div class="auth-actions">
            <button class="auth-submit" type="submit"
              :disabled="busy || !username || !password || !confirm || (auth.setupTokenRequired.value && !setupToken)">
              {{ busy ? $t('auth.setup.submitting') : $t('auth.setup.submit') }}
            </button>
          </div>
        </form>
      </template>

      <!-- Вход -->
      <template v-else-if="mode === 'login'">
        <h1>{{ $t('auth.login.title') }}</h1>
        <p v-if="auth.notice.value" class="auth-notice" role="status">{{ auth.notice.value }}</p>
        <form class="auth-form" @submit.prevent="submitLogin">
          <label class="auth-field">
            <span>{{ $t('auth.fields.username') }}</span>
            <input ref="firstInput" v-model="username" autocomplete="username" :disabled="busy" required />
          </label>
          <label class="auth-field">
            <span>{{ $t('auth.fields.password') }}</span>
            <input v-model="password" type="password" autocomplete="current-password" :disabled="busy" required />
          </label>
          <p v-if="error" class="auth-error" role="alert">{{ error }}</p>
          <div class="auth-actions">
            <button class="auth-submit" type="submit" :disabled="busy || !username || !password">
              {{ busy ? $t('auth.login.submitting') : $t('auth.login.submit') }}
            </button>
          </div>
        </form>
      </template>

      <!-- Обязательная смена временного пароля -->
      <template v-else-if="mode === 'change-password'">
        <h1>{{ $t('auth.forced.title') }}</h1>
        <p class="auth-hint">{{ $t('auth.forced.hint') }}</p>
        <ChangePasswordForm>
          <template #secondary>
            <button class="auth-link" type="button" @click="auth.logout()">{{ $t('auth.menu.logout') }}</button>
          </template>
        </ChangePasswordForm>
      </template>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import { useAuth } from '../../composables/useAuth'
import LanguageSwitcher from '../LanguageSwitcher.vue'
import ChangePasswordForm from './ChangePasswordForm.vue'
import { MIN_PASSWORD } from './constants'

const props = defineProps<{ mode: 'loading' | 'fatal' | 'setup' | 'login' | 'change-password' }>()

const { t } = useI18n()
const auth = useAuth()
const username = ref('')
const password = ref('')
const confirm = ref('')
const setupToken = ref('')
const busy = ref(false)
const error = ref('')
const firstInput = ref<HTMLInputElement | null>(null)

// Ошибки и поля не переходят между режимами; фокус — на первое поле
watch(
  () => props.mode,
  async () => {
    error.value = ''
    password.value = ''
    confirm.value = ''
    setupToken.value = ''
    await nextTick()
    firstInput.value?.focus()
  },
  { immediate: true },
)

async function submitSetup() {
  error.value = ''
  if (password.value.length < MIN_PASSWORD) {
    error.value = t('auth.errors.passwordTooShort', { min: MIN_PASSWORD })
    return
  }
  if (password.value !== confirm.value) {
    error.value = t('auth.errors.passwordMismatch')
    return
  }
  await run(() => auth.setup(username.value, password.value, setupToken.value))
}

async function submitLogin() {
  error.value = ''
  await run(() => auth.login(username.value, password.value))
}

async function run(fn: () => Promise<void>) {
  busy.value = true
  try {
    await fn()
  } catch (e) {
    error.value = errorText(e)
    password.value = ''
    confirm.value = ''
  } finally {
    busy.value = false
  }
}
</script>


<style scoped>
.auth-screen {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background:
    radial-gradient(900px 500px at 15% -10%, rgba(137, 180, 250, 0.14), transparent 60%),
    radial-gradient(700px 420px at 100% 110%, rgba(203, 166, 247, 0.10), transparent 60%),
    #11111b;
  overflow: auto;
}
.auth-card {
  width: 100%;
  max-width: 400px;
  padding: 32px 32px 28px;
  background: #181825;
  border: 1px solid #313244;
  border-radius: 16px;
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.45);
  text-align: center;
}
.auth-lang { position: absolute; top: 16px; right: 16px; }
.auth-logo { height: 36px; margin-bottom: 18px; }
h1 { margin: 0 0 8px; font-size: 20px; font-weight: 700; color: #cdd6f4; }
.auth-hint { margin: 0 0 18px; font-size: 13px; line-height: 1.5; color: #a6adc8; }
.auth-hint code { background: #11111b; padding: 1px 5px; border-radius: 4px; color: #cdd6f4; }
.auth-card > .auth-error { text-align: left; margin-bottom: 14px; }
</style>
