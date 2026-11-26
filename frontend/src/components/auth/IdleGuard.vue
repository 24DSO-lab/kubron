<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="warning" class="idle-overlay" role="alertdialog" aria-modal="true" aria-labelledby="idle-title">
    <div class="idle-dialog">
      <h3 id="idle-title">{{ $t('auth.idle.title') }}</h3>
      <i18n-t keypath="auth.idle.body" tag="p">
        <template #seconds><strong>{{ remaining }}</strong></template>
      </i18n-t>
      <div class="idle-actions">
        <button class="auth-link" type="button" @click="auth.logout()">{{ $t('auth.idle.logoutNow') }}</button>
        <button ref="continueBtn" class="auth-submit" type="button" @click="keepAlive">{{ $t('auth.idle.keepWorking') }}</button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { callApp, NOT_AUTHENTICATED } from '../../resources/wails'
import { errorText } from '../../composables/errorText'
import { useAuth } from '../../composables/useAuth'

/**
 * Автовыход по бездействию.
 *
 * «Активность» — только ввод пользователя (мышь, клавиатура, касания, прокрутка). Фоновое
 * обновление списков и метрик активностью не считается, иначе сессия не истекла бы никогда.
 * Интерфейс сообщает бэкенду об активности (ReportActivity) не чаще раза в REPORT_EVERY_MS;
 * бэкенд — окончательный арбитр и сам завершает сессию, если интерфейс завис (internal/auth).
 *
 * Таймер сверяется с настенными часами, поэтому после сна компьютера выход происходит сразу.
 */
const REPORT_EVERY_MS = 20_000
const MAX_WARNING_MS = 60_000
const TICK_MS = 1_000
const INPUT_EVENTS = ['mousemove', 'mousedown', 'keydown', 'wheel', 'touchstart', 'scroll'] as const

const auth = useAuth()
const warning = ref(false)
const remaining = ref(0)
const continueBtn = ref<HTMLButtonElement | null>(null)

const limitMs = computed(() => auth.idleTimeoutMinutes.value * 60_000)
const enabled = computed(() => !!auth.user.value && limitMs.value > 0)

let lastActivity = Date.now()
let lastReport = 0
let timer: ReturnType<typeof setInterval> | undefined

function report(force = false) {
  if (!enabled.value) return // без сессии сообщать нечего: ответ «нужен вход» вызвал бы ложный выход
  const now = Date.now()
  if (!force && now - lastReport < REPORT_EVERY_MS) return
  lastReport = now
  callApp('ReportActivity').catch(e => {
    // Бэкенд уже завершил сессию — выходим и мы
    if (errorText(e).includes(NOT_AUTHENTICATED)) auth.logout('expired')
  })
}

function onInput() {
  // Пока показано предупреждение, продлевать можно только кнопкой: случайное движение мыши не в счёт
  if (!enabled.value || warning.value) return
  lastActivity = Date.now()
  report()
}

function keepAlive() {
  warning.value = false
  lastActivity = Date.now()
  report(true)
}

function tick() {
  if (!enabled.value) {
    warning.value = false
    return
  }
  const idle = Date.now() - lastActivity
  const left = limitMs.value - idle
  if (left <= 0) {
    warning.value = false
    auth.logout('idle')
    return
  }
  const warnFor = Math.min(MAX_WARNING_MS, limitMs.value / 2)
  if (left <= warnFor) {
    remaining.value = Math.ceil(left / 1000)
    if (!warning.value) {
      warning.value = true
      void nextTick(() => continueBtn.value?.focus())
    }
  } else {
    warning.value = false
  }
}

// Вход (или смена настройки) начинает новое окно бездействия; бэкенд сам отсчитывает его от входа
watch(enabled, on => {
  if (on) {
    lastActivity = Date.now()
    lastReport = lastActivity
  }
  warning.value = false
})
watch(limitMs, () => {
  if (!enabled.value) return
  lastActivity = Date.now()
  report(true)
})

onMounted(() => {
  for (const ev of INPUT_EVENTS) window.addEventListener(ev, onInput, { passive: true, capture: true })
  timer = setInterval(tick, TICK_MS)
})
onBeforeUnmount(() => {
  for (const ev of INPUT_EVENTS) window.removeEventListener(ev, onInput, { capture: true })
  clearInterval(timer)
})
</script>

<style scoped>
.idle-overlay {
  position: fixed; inset: 0; z-index: 3000; display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.65);
}
.idle-dialog {
  width: 100%; max-width: 400px; padding: 24px; background: var(--bg-sunken);
  border: 1px solid var(--surface-0); border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5); text-align: left;
}
.idle-dialog h3 { margin: 0 0 10px; color: var(--warn); font-size: 17px; }
.idle-dialog p { margin: 0 0 18px; color: var(--text-muted); font-size: 13px; line-height: 1.5; }
.idle-dialog strong { color: var(--text); }
.idle-actions { display: flex; justify-content: flex-end; align-items: center; gap: 10px; }
</style>

