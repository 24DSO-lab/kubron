// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeAll, describe, expect, it } from 'vitest'
import { setLocale } from './index'
import {
  ARGO_TOKEN_NOT_SET_MESSAGE,
  NOT_AUTHENTICATED_MESSAGE,
  isArgoTokenMissing,
  isNotAuthenticated,
  resetBackendErrorCache,
  translateBackendError,
} from './backendErrors'

describe('перевод ошибок бэкенда', () => {
  beforeAll(async () => {
    await setLocale('ru', { persist: false })
    resetBackendErrorCache()
  })

  it('переводит простое сообщение', () => {
    expect(translateBackendError(NOT_AUTHENTICATED_MESSAGE)).toBe('требуется вход в систему')
  })

  it('переводит префикс составной ошибки и оставляет причину как есть', () => {
    expect(translateBackendError('failed to save token: permission denied')).toBe('не удалось сохранить токен: permission denied')
  })

  it('переводит вложенные ошибки по частям', () => {
    const out = translateBackendError('failed to load kubeconfig: failed to read journal: open x: no such file')
    expect(out).toBe('не удалось загрузить kubeconfig: не удалось прочитать журнал: open x: no such file')
  })

  it('подставляет параметры', () => {
    const out = translateBackendError('failed to apply Deployment web: boom')
    expect(out).toContain('Deployment')
    expect(out).toContain('web')
    expect(out).toContain('boom')
    expect(out).not.toContain('failed to')
  })

  it('переводит ошибки отладочных контейнеров и оставляет причину от кластера как есть', () => {
    expect(translateBackendError('debug containers are disabled by the administrator')).toBe('отладочные контейнеры отключены администратором')
    expect(translateBackendError('image "evil" is not allowed by the administrator (allowed: busybox:1)')).toBe(
      'образ "evil" не разрешён администратором (разрешены: busybox:1)',
    )
    const out = translateBackendError(
      'the cluster account is not allowed to add ephemeral containers to pod prod/web (RBAC: patch on pods/ephemeralcontainers): denied',
    )
    expect(out).toContain('prod/web')
    expect(out).toContain('pods/ephemeralcontainers')
    expect(out.endsWith('): denied')).toBe(true)
    expect(out).not.toContain('not allowed')
  })

  it('неизвестное сообщение возвращает как есть', () => {
    expect(translateBackendError('the server could not find the requested resource')).toBe('the server could not find the requested resource')
    expect(translateBackendError('')).toBe('')
  })

  it('на английском сообщения не меняются', async () => {
    await setLocale('en', { persist: false })
    expect(translateBackendError(NOT_AUTHENTICATED_MESSAGE)).toBe(NOT_AUTHENTICATED_MESSAGE)
    await setLocale('ru', { persist: false })
  })

  it('решения принимаются по английскому тексту, а не по переводу', () => {
    expect(isNotAuthenticated(`call failed: ${NOT_AUTHENTICATED_MESSAGE}`)).toBe(true)
    expect(isArgoTokenMissing(ARGO_TOKEN_NOT_SET_MESSAGE)).toBe(true)
    expect(isArgoTokenMissing(translateBackendError(ARGO_TOKEN_NOT_SET_MESSAGE))).toBe(false)
  })
})
