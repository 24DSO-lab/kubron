// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref } from 'vue'
import { callApp } from '../platform/api'
import { can } from './useAuth'
import { Perm } from '../types/auth'
import type { UpdateInfo, VersionInfo } from '../types/update'

/**
 * Версия сборки и обновления (ADR-0028). Версию видит любой вошедший пользователь, состояние обновлений и команды —
 * администратор. Бэкенд ничего не устанавливает: интерфейс показывает, что вышло и как обновиться для способа установки.
 */

const DISMISSED_KEY = 'kubron.update.dismissed'
/** Как часто интерфейс перечитывает состояние у бэкенда (сеть при этом не используется: это кэш проверки) */
const REFRESH_MS = 60 * 60 * 1000

const version = ref<VersionInfo | null>(null)
const info = ref<UpdateInfo | null>(null)
const checking = ref(false)
/** Код ошибки ручной проверки (когда ответа бэкенда нет совсем) */
const failure = ref('')
const dismissedFor = ref(readDismissed())
/** Окно обновления открыто (одно на приложение: его открывают бейдж в шапке, баннер и настройки) */
const dialogOpen = ref(false)
let timer: ReturnType<typeof setInterval> | null = null

function readDismissed(): string {
  try {
    return localStorage.getItem(DISMISSED_KEY) ?? ''
  } catch {
    return ''
  }
}

/** Приводит ответ бэкенда к ожидаемой форме: без обязательных полей интерфейс не должен падать. */
export function normalizeInfo(raw: unknown): UpdateInfo | null {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as Partial<UpdateInfo>
  return {
    ...r,
    current: typeof r.current === 'string' ? r.current : '',
    install: {
      target: r.install?.target ?? '',
      method: r.install?.method ?? 'unknown',
      os: r.install?.os ?? '',
      arch: r.install?.arch ?? '',
      helmRelease: r.install?.helmRelease,
      helmNamespace: r.install?.helmNamespace,
    },
    locked: !!r.locked,
    autoCheck: !!r.autoCheck,
    available: !!r.available && !!r.latest,
    commands: Array.isArray(r.commands) ? r.commands : [],
  }
}

/** Нужно ли показывать баннер: обновление есть и именно эту версию пользователь ещё не скрыл. */
export function shouldShowBanner(i: UpdateInfo | null, dismissed: string): boolean {
  return !!i && i.available && !!i.latest && i.latest !== dismissed
}

/** Версия для показа: «1.2.0» → «v1.2.0», «dev» остаётся как есть. */
export function displayVersion(v: string | undefined | null): string {
  if (!v) return ''
  return /^\d/.test(v) ? `v${v}` : v
}

/** Читает версию (всем) и состояние обновлений (администратору). Не бросает исключений. */
async function load(): Promise<void> {
  try {
    version.value = (await callApp('GetVersionInfo')) as VersionInfo
  } catch {
    /* без версии бейдж просто не рисуется */
  }
  if (!can(Perm.UsersManage)) {
    info.value = null
    return
  }
  try {
    info.value = normalizeInfo(await callApp('GetUpdateStatus'))
  } catch {
    /* состояние обновлений — удобство: сбой не должен мешать работе */
  }
}

export function useUpdates() {
  /** Загружает данные и начинает их периодически перечитывать; повторный вызов безопасен. */
  async function start() {
    await load()
    if (!timer) timer = setInterval(() => void load(), REFRESH_MS)
  }

  function stop() {
    if (timer) clearInterval(timer)
    timer = null
    version.value = null
    info.value = null
    failure.value = ''
    dialogOpen.value = false
  }

  /** Ручная проверка (кнопка «Проверить»); возвращает свежее состояние. */
  async function checkNow() {
    if (checking.value) return
    checking.value = true
    failure.value = ''
    try {
      info.value = normalizeInfo(await callApp('CheckForUpdates'))
    } catch (e) {
      failure.value = e instanceof Error ? e.message : String(e)
    } finally {
      checking.value = false
    }
  }

  /** Включает или выключает фоновую проверку (только десктоп); ошибку бросает вызывающему. */
  async function setAutoCheck(enabled: boolean) {
    await callApp('SetUpdateCheckEnabled', enabled)
    if (info.value) info.value = { ...info.value, autoCheck: enabled }
  }

  /** Скрывает баннер для текущей новой версии; на следующую версию он появится снова. */
  function dismissBanner() {
    const latest = info.value?.latest ?? ''
    dismissedFor.value = latest
    try {
      localStorage.setItem(DISMISSED_KEY, latest)
    } catch {
      /* хранилище недоступно: баннер скрыт до перезагрузки */
    }
  }

  function openDialog() { dialogOpen.value = true }
  function closeDialog() { dialogOpen.value = false }

  const bannerVisible = computed(() => shouldShowBanner(info.value, dismissedFor.value))
  const updateAvailable = computed(() => !!info.value?.available)

  return {
    version: readonly(version),
    info: readonly(info),
    checking: readonly(checking),
    failure: readonly(failure),
    dialogOpen: readonly(dialogOpen),
    openDialog,
    closeDialog,
    bannerVisible,
    updateAvailable,
    start,
    stop,
    checkNow,
    setAutoCheck,
    dismissBanner,
  }
}
