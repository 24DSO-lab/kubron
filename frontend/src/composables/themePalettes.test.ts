// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { chartPalette, monacoThemeName, terminalPalette } from './themePalettes'

describe('monacoThemeName', () => {
  it('тёмная — vs-dark, светлая — vs', () => {
    expect(monacoThemeName('dark')).toBe('vs-dark')
    expect(monacoThemeName('light')).toBe('vs')
  })
})

describe('terminalPalette', () => {
  it('тёмная тема сохраняет прежние цвета терминала', () => {
    expect(terminalPalette('dark')).toEqual({ background: '#1e1e1e', foreground: '#d4d4d4', cursor: '#00ff00', cursorAccent: '#00ff00' })
  })
  it('светлая тема задаёт фон, текст и полную ANSI-палитру', () => {
    const p = terminalPalette('light')
    expect(p.background).toBe('#eff1f5')
    expect(p.foreground).not.toBe(p.background)
    for (const k of ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
      'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite'] as const) {
      expect(p[k], k).toMatch(/^#[0-9a-f]{6}$/)
    }
  })
})

describe('chartPalette', () => {
  it('тёмная тема сохраняет прежние цвета графиков', () => {
    expect(chartPalette('dark')).toEqual({
      tooltipBackground: '#1F2937', tooltipTitle: '#F9FAFB', tooltipBody: '#D1D5DB',
      tooltipBorder: '#374151', grid: '#1F2937', tick: '#6B7280',
    })
  })
  it('в светлой теме подсказка светлая, а сетка и подписи отличаются от тёмной', () => {
    const l = chartPalette('light'), d = chartPalette('dark')
    expect(l.tooltipBackground).toBe('#ffffff')
    for (const k of Object.keys(l) as (keyof typeof l)[]) expect(l[k], k).not.toBe(d[k])
  })
})
