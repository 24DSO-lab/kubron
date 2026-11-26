// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { renderWithinLimit, screenshotScale } from '../components/bugreport/bugReportView'

/** Элементы с этим атрибутом в снимок не попадают (например, всплывающие окна самого отчёта) */
export const SCREENSHOT_IGNORE_ATTR = 'data-screenshot-ignore'

/**
 * Снимок видимой области окна как PNG data URL. Рисуется из DOM (SVG foreignObject), поэтому системные окна
 * и содержимое за пределами веб-вью в него не попадают. Библиотека подгружается при первом вызове: в основной
 * бандл она не входит.
 */
export async function captureScreenshot(): Promise<string> {
  const { domToPng } = await import('modern-screenshot')
  const width = window.innerWidth
  const height = window.innerHeight
  return renderWithinLimit(
    (scale) => domToPng(document.documentElement, {
      width,
      height,
      scale,
      backgroundColor: pageBackground(),
      timeout: 15000,
      filter: (node) => !(node instanceof Element && node.hasAttribute(SCREENSHOT_IGNORE_ATTR)),
    }),
    screenshotScale(width, height, window.devicePixelRatio),
  )
}

/** Фон снимка = фон страницы в текущей теме (canvas не понимает var(--bg), нужен готовый цвет) */
function pageBackground(): string {
  try {
    return getComputedStyle(document.documentElement).getPropertyValue('--bg').trim() || '#1e1e2e'
  } catch {
    return '#1e1e2e'
  }
}
