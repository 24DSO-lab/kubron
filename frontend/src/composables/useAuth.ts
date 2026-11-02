// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref } from 'vue'
import { callApp, onSessionLost, setSetupToken } from '../resources/wails'
import type { Permission, UserInfo } from '../types/auth'
import { errorText } from './errorText'
import { t } from '../i18n'
import { loadCapabilities } from './useCapabilities'

/**
 * Состояние аутентификации — одно на всё приложение (модульный синглтон).
 *
 * Важно: это только удобство интерфейса. Права на самом деле проверяет Go-бэкенд
 * (internal/auth) в каждом методе; скрытая кнопка безопасность не обеспечивает.
 */

/** Экран, который сейчас должен показывать приложение */
export type AuthScreen = 'loading' | 'fatal' | 'setup' | 'login' | 'change-password' | 'app'

const initialized = ref(false)
const needsSetup = ref(false)
/** Первичная настройка по сети требует токен (KUBRON_SETUP_TOKEN, сервисный режим) */
const setupTokenRequired = ref(false)
const user = ref<UserInfo | null>(null)
const fatalError = ref('')
/** Через сколько минут без действий сессия завершается; 0 — не завершается */
const idleTimeoutMinutes = ref(0)
/** Сообщение на экране входа (например, «Сеанс завершён из-за бездействия») */
const notice = ref('')

/** Почему сессия завершилась: запоминается на время перезагрузки интерфейса */
export type LogoutReason = 'idle' | 'expired'
const LOGOUT_REASON_KEY = 'kubron.logoutReason'
/** Ключи сообщений: текст берётся в момент показа (язык может смениться) */
const LOGOUT_NOTICE_KEYS: Record<LogoutReason, string> = {
  idle: 'auth.notice.idle',
  expired: 'auth.notice.expired',
}

function rememberLogoutReason(reason: LogoutReason) {
  try {
    sessionStorage.setItem(LOGOUT_REASON_KEY, reason)
  } catch {
    /* хранилище недоступно — сообщение просто не покажется */
  }
}

function takeLogoutNotice(): string {
  try {
    const reason = sessionStorage.getItem(LOGOUT_REASON_KEY) as LogoutReason | null
    sessionStorage.removeItem(LOGOUT_REASON_KEY)
    return reason && LOGOUT_NOTICE_KEYS[reason] ? t(LOGOUT_NOTICE_KEYS[reason]) : ''
  } catch {
    return ''
  }
}

let loggingOut = false

const screen = computed<AuthScreen>(() => {
  if (fatalError.value) return 'fatal'
  if (!initialized.value) return 'loading'
  if (needsSetup.value) return 'setup'
  if (!user.value) return 'login'
  if (user.value.mustChangePassword) return 'change-password'
  return 'app'
})

/** Текст ошибки из Wails (строка или Error) на языке интерфейса; единая реализация — composables/errorText.ts */
export { errorText }

/** Права вошедшего пользователя; пока никто не вошёл — ничего не разрешено */
export function can(permission: Permission): boolean {
  const u = user.value
  return !!u && !u.mustChangePassword && u.permissions.includes(permission)
}

export function useAuth() {
  async function init() {
    try {
      notice.value = takeLogoutNotice()
      void loadCapabilities() // метод публичный и не блокирует вход; ошибки гасит сам
      const st = await callApp('AuthStatus')
      idleTimeoutMinutes.value = st.idleTimeoutMinutes ?? 0
      needsSetup.value = !!st.needsSetup
      setupTokenRequired.value = !!(st as unknown as Record<string, unknown>).setupTokenRequired
      user.value = (st.user as UserInfo | null) ?? null
      fatalError.value = ''
    } catch (e) {
      fatalError.value = errorText(e)
    } finally {
      initialized.value = true
    }
  }

  /** Настройка автовыхода хранится на бэкенде; после входа читаем актуальное значение */
  async function refreshIdleTimeout() {
    try {
      idleTimeoutMinutes.value = (await callApp('AuthStatus')).idleTimeoutMinutes ?? 0
    } catch {
      /* без значения автовыход просто не включится до следующего запуска */
    }
  }

  async function setup(username: string, password: string, token = '') {
    await setSetupToken(setupTokenRequired.value ? token : '')
    user.value = (await callApp('SetupAdmin', username.trim(), password)) as UserInfo
    needsSetup.value = false
    notice.value = ''
    await refreshIdleTimeout()
  }

  async function login(username: string, password: string) {
    user.value = (await callApp('Login', username.trim(), password)) as UserInfo
    notice.value = ''
    await refreshIdleTimeout()
  }

  /** Администратор: время автовыхода в минутах, 0 — выключить */
  async function setIdleTimeout(minutes: number) {
    idleTimeoutMinutes.value = await callApp('SetIdleTimeout', minutes)
  }

  async function logout(reason?: LogoutReason) {
    if (loggingOut) return
    loggingOut = true
    if (reason) rememberLogoutReason(reason)
    try {
      await callApp('Logout')
    } finally {
      // Перезагрузка сбрасывает всё состояние интерфейса (кластеры, вкладки, кэши списков):
      // следующий пользователь не увидит данных предыдущего.
      window.location.reload()
    }
  }

  async function changePassword(oldPassword: string, newPassword: string) {
    await callApp('ChangePassword', oldPassword, newPassword)
    user.value = (await callApp('CurrentUser')) as UserInfo
  }

  return {
    screen: readonly(screen),
    user: readonly(user),
    setupTokenRequired: readonly(setupTokenRequired),
    fatalError: readonly(fatalError),
    idleTimeoutMinutes: readonly(idleTimeoutMinutes),
    notice: readonly(notice),
    init,
    setup,
    login,
    logout,
    changePassword,
    setIdleTimeout,
    can,
  }
}

// Бэкенд сообщил, что сессии больше нет (истекла по бездействию, пока интерфейс не успел выйти сам)
onSessionLost(() => {
  if (user.value) void useAuth().logout('expired')
})
