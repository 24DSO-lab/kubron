// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Предел длины, как в Go (maxArgoURLLen). */
export const MAX_ARGO_URL = 512

/**
 * Адрес Argo CD из настроек кластера: пусто или http(s) с хостом, без учётных данных, query и fragment
 * (те же правила, что у бэкенда; настоящую проверку делает он).
 */
export function isValidArgoUrl(value: string): boolean {
  const v = value.trim()
  if (v === '') return true
  if (v.length > MAX_ARGO_URL) return false
  try {
    const u = new URL(v)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname !== '' &&
      u.username === '' && u.password === '' && u.search === '' && u.hash === '' && !v.includes('#') && !v.includes('?')
  } catch {
    return false
  }
}
