// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { GKEOAuthStatus } from '../../types/credential'

/** Те же границы, что у gke.NewWithUserOAuth: печатные ASCII без пробелов, не длиннее 256 */
const MAX_FIELD = 256
const printable = (s: string) => /^[\x21-\x7e]*$/.test(s)

/** Идентификатор клиента годится для сохранения. Окончательную проверку делает бэкенд. */
export function validClientId(id: string): boolean {
  const v = id.trim()
  return v.length > 0 && v.length <= MAX_FIELD && printable(v)
}

/** Секрет может быть пустым (оставить сохранённый или без секрета) */
export function validClientSecret(secret: string): boolean {
  return secret.length <= MAX_FIELD && printable(secret)
}

export function canSaveGKEOAuth(clientId: string, clientSecret: string): boolean {
  return validClientId(clientId) && validClientSecret(clientSecret)
}

/** Ключ строки состояния в credential.gkeOAuth.state.* */
export function gkeOAuthStateKey(s: GKEOAuthStatus | null): 'unknown' | 'notConfigured' | 'configured' | 'restart' {
  if (!s) return 'unknown'
  if (s.restartRequired) return 'restart'
  return s.configured ? 'configured' : 'notConfigured'
}
