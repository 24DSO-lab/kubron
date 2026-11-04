// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Тексты бэкенда на английском и их перевод на выбранный язык.
 *
 * Go-код возвращает ошибки только по-английски (errors.New / fmt.Errorf); перевод делает
 * фронтенд. Таблицы отдельной нет: английские шаблоны сообщений — это значения
 * `backend.*` в locales/en/backend.json, а перевод — те же ключи в locales/<язык>/backend.json.
 * Шаблон — формат Go с плейсхолдерами {p0}, {p1}… вместо %s/%d/%q/%v по порядку. Неизвестное
 * сообщение (в том числе тексты client-go и сервера Kubernetes) показывается как есть: английский
 * всегда читаем.
 */
import { DEFAULT_LOCALE } from './languages'
import { i18n, t } from './index'

/** Сообщения, по которым фронтенд принимает решения. Это не перевод: тексты должны совпадать с Go */
/** auth.ErrNotAuthenticated */
export const NOT_AUTHENTICATED_MESSAGE = 'authentication required'
/** k8s.ErrArgoTokenNotSet */
export const ARGO_TOKEN_NOT_SET_MESSAGE = 'Argo CD API token is not set'

/** credential.ErrAnswerRejected, ErrChallengeExpired, ErrInvalidGrant (internal/credential/contract.go) */
export const ANSWER_REJECTED_MESSAGE = 'the credentials were rejected'
export const CHALLENGE_EXPIRED_MESSAGE = 'the sign-in request has expired'
export const INVALID_GRANT_MESSAGE = 'the saved sign-in is no longer valid'

export const isAnswerRejected = (message: string) => message.includes(ANSWER_REJECTED_MESSAGE)
export const isChallengeExpired = (message: string) => message.includes(CHALLENGE_EXPIRED_MESSAGE)
export const isInvalidGrant = (message: string) => message.includes(INVALID_GRANT_MESSAGE)

export const isNotAuthenticated = (message: string) => message.includes(NOT_AUTHENTICATED_MESSAGE)
export const isArgoTokenMissing = (message: string) => message.includes(ARGO_TOKEN_NOT_SET_MESSAGE)

interface Template {
  key: string
  /** Длина литеральной части шаблона (без плейсхолдеров): чем больше, тем он конкретнее */
  specificity: number
  /** Сообщение целиком (Go-формат %w в конце → wrapped=true: хвост переводится рекурсивно) */
  regex: RegExp
  wrapped: boolean
}

let compiled: Template[] | null = null

const escapeRegex = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/** Значение en-шаблона без экранирования vue-i18n: {'{'} → { */
const unescapeMessage = (s: string) => s.replace(/\{'(.)'\}/g, '$1')

function compile(): Template[] {
  const messages = i18n.global.getLocaleMessage(DEFAULT_LOCALE) as unknown as { backend?: Record<string, string> }
  const out: Template[] = []
  for (const [key, raw] of Object.entries(messages.backend ?? {})) {
    const text = unescapeMessage(raw)
    // Последний плейсхолдер после «: » — это обёрнутая ошибка (%w): «failed to read: <причина>»
    const wrapped = /: \{p\d+\}$/.test(text)
    const pattern = text
      .split(/(\{p\d+\})/)
      .map((part) => (/^\{p\d+\}$/.test(part) ? '(.+?)' : escapeRegex(part)))
      .join('')
    const specificity = text.replace(/\{p\d+\}/g, '').length
    out.push({ key, specificity, regex: new RegExp(`^${wrapped ? pattern.replace(/\(\.\+\?\)$/, '(.+)') : pattern}$`, 's'), wrapped })
  }
  // Конкретные шаблоны сопоставляются раньше общих («failed to read token: …» раньше «failed to read {p0}: …»)
  return out.sort((a, b) => b.specificity - a.specificity)
}

/**
 * Перевести сообщение бэкенда на текущий язык интерфейса. Составные ошибки `<префикс>: <причина>`
 * переводятся по частям: префикс — по таблице, причина — рекурсивно (или как есть).
 */
export function translateBackendError(message: string, depth = 0): string {
  if (!message || depth > 4) return message
  compiled ??= compile()
  for (const tpl of compiled) {
    const m = tpl.regex.exec(message)
    if (!m) continue
    const params: Record<string, string> = {}
    m.slice(1).forEach((value, i) => {
      // Только обёрнутая ошибка (последняя группа в wrapped-шаблоне) — переводимый текст
      const last = i === m.length - 2
      params[`p${i}`] = tpl.wrapped && last ? translateBackendError(value, depth + 1) : value
    })
    return t(`backend.${tpl.key}`, params)
  }
  return message
}

/** Только для тестов: сбросить кеш скомпилированных шаблонов (после подмены сообщений) */
export function resetBackendErrorCache() {
  compiled = null
}
