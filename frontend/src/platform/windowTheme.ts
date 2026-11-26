// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { WindowSetDarkTheme, WindowSetLightTheme } from '../../wailsjs/runtime/runtime'

/**
 * Тема оконной рамки (заголовок и кнопки) в десктопной версии: в Windows её рисует система, а не страница.
 * В вебе рамки нет, там вызов ничего не делает.
 */
export function setWindowTheme(theme: 'dark' | 'light'): void {
  if (import.meta.env.VITE_TARGET === 'web') return
  try {
    if (theme === 'light') WindowSetLightTheme()
    else WindowSetDarkTheme()
  } catch {
    // runtime недоступен (тесты, нестандартная оболочка) — рамка просто останется прежней
  }
}
