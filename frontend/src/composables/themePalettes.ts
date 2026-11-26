// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { ResolvedTheme } from './useTheme'

// Палитры сторонних компонентов (Monaco, xterm, Chart.js): они рисуют в canvas или во внутренних
// слоях и не видят CSS-переменных, поэтому цвета для каждой темы заданы здесь. Тёмные значения —
// те, что использовались до появления светлой темы; светлые взяты из палитры Catppuccin Latte.

/** Имя встроенной темы Monaco */
export function monacoThemeName(theme: ResolvedTheme): 'vs' | 'vs-dark' {
  return theme === 'light' ? 'vs' : 'vs-dark'
}

/** Совместима с ITheme из @xterm/xterm; тип объявлен здесь, чтобы модуль не тянул xterm в тесты */
export interface TerminalPalette {
  background: string
  foreground: string
  cursor: string
  cursorAccent: string
  selectionBackground?: string
  black?: string
  red?: string
  green?: string
  yellow?: string
  blue?: string
  magenta?: string
  cyan?: string
  white?: string
  brightBlack?: string
  brightRed?: string
  brightGreen?: string
  brightYellow?: string
  brightBlue?: string
  brightMagenta?: string
  brightCyan?: string
  brightWhite?: string
}

export function terminalPalette(theme: ResolvedTheme): TerminalPalette {
  if (theme === 'light') {
    // Явная ANSI-палитра нужна: стандартные цвета xterm рассчитаны на тёмный фон и на светлом нечитаемы
    return {
      background: '#eff1f5',
      foreground: '#4c4f69',
      cursor: '#4c4f69',
      cursorAccent: '#eff1f5',
      selectionBackground: 'rgba(76, 79, 105, 0.25)',
      black: '#5c5f77',
      red: '#d20f39',
      green: '#40a02b',
      yellow: '#df8e1d',
      blue: '#1e66f5',
      magenta: '#ea76cb',
      cyan: '#179299',
      white: '#acb0be',
      brightBlack: '#6c6f85',
      brightRed: '#d20f39',
      brightGreen: '#40a02b',
      brightYellow: '#df8e1d',
      brightBlue: '#1e66f5',
      brightMagenta: '#ea76cb',
      brightCyan: '#179299',
      brightWhite: '#bcc0cc',
    }
  }
  return { background: '#1e1e1e', foreground: '#d4d4d4', cursor: '#00ff00', cursorAccent: '#00ff00' }
}

export interface ChartPalette {
  tooltipBackground: string
  tooltipTitle: string
  tooltipBody: string
  tooltipBorder: string
  grid: string
  tick: string
}

export function chartPalette(theme: ResolvedTheme): ChartPalette {
  if (theme === 'light') {
    return {
      tooltipBackground: '#ffffff',
      tooltipTitle: '#4c4f69',
      tooltipBody: '#5c5f77',
      tooltipBorder: '#ccd0da',
      grid: '#dce0e8',
      tick: '#7c7f93',
    }
  }
  return {
    tooltipBackground: '#1F2937',
    tooltipTitle: '#F9FAFB',
    tooltipBody: '#D1D5DB',
    tooltipBorder: '#374151',
    grid: '#1F2937',
    tick: '#6B7280',
  }
}
