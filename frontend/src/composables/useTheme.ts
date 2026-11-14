// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref } from 'vue'
import { setWindowTheme } from '../platform/windowTheme'

/** Выбор пользователя: фиксированная тема или следование настройке ОС */
export type ThemeMode = 'dark' | 'light' | 'system'
/** Тема, которая реально применена к интерфейсу */
export type ResolvedTheme = 'dark' | 'light'

export const THEME_MODES: readonly ThemeMode[] = ['dark', 'light', 'system']

const STORAGE_KEY = 'kubron.theme'
// Пока светлая тема дорабатывается, по умолчанию остаётся тёмная; после доработки можно сменить на 'system'
const DEFAULT_MODE: ThemeMode = 'dark'
const LIGHT_QUERY = '(prefers-color-scheme: light)'

export function parseThemeMode(raw: unknown): ThemeMode {
  return typeof raw === 'string' && (THEME_MODES as readonly string[]).includes(raw) ? (raw as ThemeMode) : DEFAULT_MODE
}

export function resolveTheme(mode: ThemeMode, systemPrefersLight: boolean): ResolvedTheme {
  if (mode === 'system') return systemPrefersLight ? 'light' : 'dark'
  return mode
}

const mode = ref<ThemeMode>(DEFAULT_MODE)
const systemPrefersLight = ref(false)
const resolved = computed<ResolvedTheme>(() => resolveTheme(mode.value, systemPrefersLight.value))
let initialized = false

function applyToDocument(): void {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.theme = resolved.value
  setWindowTheme(resolved.value)
}

/**
 * Читает сохранённый выбор и применяет тему. Вызывается один раз до монтирования приложения:
 * иначе на долю секунды мелькнёт тёмная тема. Повторные вызовы ничего не делают.
 */
export function initTheme(): void {
  if (initialized) return
  initialized = true
  try {
    mode.value = parseThemeMode(localStorage.getItem(STORAGE_KEY))
  } catch {
    // localStorage недоступен (приватный режим, тесты) — остаёмся на теме по умолчанию
  }
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    const query = window.matchMedia(LIGHT_QUERY)
    systemPrefersLight.value = query.matches
    query.addEventListener('change', (e) => {
      systemPrefersLight.value = e.matches
      applyToDocument()
    })
  }
  applyToDocument()
}

function setMode(next: ThemeMode): void {
  mode.value = parseThemeMode(next)
  try {
    localStorage.setItem(STORAGE_KEY, mode.value)
  } catch {
    // выбор не сохранится до следующего запуска — не критично
  }
  applyToDocument()
}

export function useTheme() {
  return {
    mode: readonly(mode),
    /** Применённая тема: для Monaco, xterm и графиков, которым нужно знать «тёмная или светлая» */
    resolved,
    setMode,
  }
}
