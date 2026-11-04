// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { Challenge, ChallengeField, ChallengeKind } from '../../types/credential'

/** Поля формы вызова. Для credentials набор задан контрактом: username, password. */
export function challengeFields(c: Challenge): ChallengeField[] {
  if (c.kind === 'credentials') {
    return c.fields?.length ? c.fields : [{ name: 'username', secret: false }, { name: 'password', secret: true }]
  }
  if (c.kind === 'paste-redirect') return [{ name: 'redirectUrl', secret: false }]
  if (c.kind === 'manual-token') return [{ name: 'token', secret: true }]
  if (c.kind === 'secret-fields') return c.fields ?? []
  return []
}

/** Виды вызова, у которых есть форма с кнопкой «Отправить» */
const FORM_KINDS: ReadonlySet<ChallengeKind> = new Set(['credentials', 'paste-redirect', 'secret-fields', 'manual-token'])
export const hasForm = (kind: ChallengeKind) => FORM_KINDS.has(kind)

/** Виды вызова с ожиданием: диалог опрашивает бэкенд и закрывается сам */
export const isWaiting = (c: Challenge) => c.kind === 'device-code' || c.kind === 'browser-loopback' || c.kind === 'browser-callback'

/** Все обязательные поля заполнены (пробелы в секретах значимы, в остальных полях — нет). */
export function fieldsFilled(fields: ChallengeField[], values: Record<string, string>): boolean {
  if (fields.length === 0) return false
  return fields.every((f) => (f.secret ? (values[f.name] ?? '').length > 0 : (values[f.name] ?? '').trim().length > 0))
}

/** Секунд до срока (не меньше нуля). Некорректная дата считается истёкшей. */
export function secondsLeft(expiresAt: string, now: number): number {
  const t = Date.parse(expiresAt)
  if (!Number.isFinite(t)) return 0
  return Math.max(0, Math.ceil((t - now) / 1000))
}

/** «m:ss» для обратного отсчёта */
export function formatCountdown(totalSec: number): string {
  const s = Math.max(0, Math.floor(totalSec))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/** Интервал опроса: значение бэкенда, не реже раза в минуту и не чаще раза в секунду; по умолчанию 5 с. */
export function pollIntervalMs(intervalSec: number | undefined): number {
  const sec = intervalSec && intervalSec > 0 ? intervalSec : 5
  return Math.min(60, Math.max(1, sec)) * 1000
}

const SAFE_WORD = /^[A-Za-z0-9_@%+=:,./-]+$/

/** Слово для показа в командной строке POSIX-оболочки (одинарные кавычки). Только отображение. */
export function shellQuoteWord(word: string): string {
  if (word === '') return "''"
  if (SAFE_WORD.test(word)) return word
  return `'${word.replace(/'/g, `'\\''`)}'`
}

/** Команда для показа и копирования: слова с экранированием. Никогда не выполняется. */
export function commandLine(words: readonly string[] | undefined | null): string {
  return (words ?? []).map(shellQuoteWord).join(' ')
}

/** Команда из exec-блока для показа */
export function execCommandLine(exec: { command: string; args: string[] | null }): string {
  return commandLine([exec.command, ...(exec.args ?? [])])
}

/** Ссылка для открытия в браузере: только http(s), без javascript: и подобных */
export function safeHref(url: string | undefined): string {
  if (!url) return ''
  try {
    const u = new URL(url)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.toString() : ''
  } catch {
    return ''
  }
}
