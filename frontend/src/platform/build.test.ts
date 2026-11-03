// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { RELOAD_GUARD_MS, mayReload, withBuild } from './build'

describe('withBuild', () => {
  it('добавляет версию в адрес WebSocket', () => {
    expect(withBuild('wss://h/api/ws', '1.2.0')).toBe('wss://h/api/ws?build=1.2.0')
    expect(withBuild('wss://h/api/ws?x=1', '1.2.0')).toBe('wss://h/api/ws?x=1&build=1.2.0')
  })
  it('экранирует версию', () => {
    expect(withBuild('wss://h/api/ws', '1.2.0+meta')).toBe('wss://h/api/ws?build=1.2.0%2Bmeta')
  })
  it('dev и пустая версия адрес не меняют', () => {
    expect(withBuild('wss://h/api/ws', 'dev')).toBe('wss://h/api/ws')
    expect(withBuild('wss://h/api/ws', '')).toBe('wss://h/api/ws')
  })
})

describe('mayReload', () => {
  it('первая перезагрузка разрешена', () => {
    expect(mayReload(1000, null)).toBe(true)
    expect(mayReload(1000, Number.NaN)).toBe(true)
  })
  it('повторная в пределах интервала запрещена', () => {
    expect(mayReload(1000 + RELOAD_GUARD_MS - 1, 1000)).toBe(false)
    expect(mayReload(1000 + RELOAD_GUARD_MS, 1000)).toBe(true)
  })
})
