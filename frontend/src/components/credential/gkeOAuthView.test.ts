// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { canSaveGKEOAuth, gkeOAuthStateKey, validClientId, validClientSecret } from './gkeOAuthView'

const st = (o: Partial<{ configured: boolean; clientId: string; secretSet: boolean; restartRequired: boolean }> = {}) =>
  ({ configured: false, clientId: '', secretSet: false, restartRequired: false, ...o })

describe('validClientId', () => {
  it('принимает обычный идентификатор Google', () => {
    expect(validClientId('1234567890-abcdef.apps.googleusercontent.com')).toBe(true)
    expect(validClientId('  1-a.apps.googleusercontent.com  ')).toBe(true) // пробелы по краям обрезаются при сохранении
  })
  it('не принимает пустое, с пробелами внутри, с переносом и слишком длинное', () => {
    for (const bad of ['', '   ', 'a b', 'a\nb', 'идентификатор', 'x'.repeat(257)]) expect(validClientId(bad)).toBe(false)
  })
})

describe('validClientSecret', () => {
  it('пустой секрет допустим (оставить сохранённый)', () => expect(validClientSecret('')).toBe(true))
  it('печатные ASCII без пробелов, не длиннее 256', () => {
    expect(validClientSecret('GOCSPX-abc_def')).toBe(true)
    for (const bad of ['a b', 'a\nb', 'секрет', 'x'.repeat(257)]) expect(validClientSecret(bad)).toBe(false)
  })
})

describe('canSaveGKEOAuth', () => {
  it('нужен идентификатор; секрет необязателен', () => {
    expect(canSaveGKEOAuth('1-a.apps.googleusercontent.com', '')).toBe(true)
    expect(canSaveGKEOAuth('', 'GOCSPX-x')).toBe(false)
    expect(canSaveGKEOAuth('1-a.apps.googleusercontent.com', 'a b')).toBe(false)
  })
})

describe('gkeOAuthStateKey', () => {
  it('выбирает строку состояния', () => {
    expect(gkeOAuthStateKey(null)).toBe('unknown')
    expect(gkeOAuthStateKey(st())).toBe('notConfigured')
    expect(gkeOAuthStateKey(st({ configured: true, clientId: 'x' }))).toBe('configured')
    expect(gkeOAuthStateKey(st({ configured: true, restartRequired: true }))).toBe('restart')
    expect(gkeOAuthStateKey(st({ restartRequired: true }))).toBe('restart') // клиент удалён у работающего broker
  })
})
