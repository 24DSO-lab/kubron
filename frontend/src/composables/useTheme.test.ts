// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { parseThemeMode, resolveTheme, THEME_MODES } from './useTheme'

describe('parseThemeMode', () => {
  it('допустимые значения возвращаются как есть', () => {
    for (const m of THEME_MODES) expect(parseThemeMode(m)).toBe(m)
  })
  it('мусор, пусто и не-строки дают тему по умолчанию (тёмную)', () => {
    for (const v of [null, undefined, '', 'Light', 'blue', 1, {}, ['light']]) {
      expect(parseThemeMode(v), String(v)).toBe('dark')
    }
  })
})

describe('resolveTheme', () => {
  it('фиксированная тема не зависит от настройки ОС', () => {
    expect(resolveTheme('dark', true)).toBe('dark')
    expect(resolveTheme('light', false)).toBe('light')
  })
  it('system следует настройке ОС', () => {
    expect(resolveTheme('system', true)).toBe('light')
    expect(resolveTheme('system', false)).toBe('dark')
  })
})
