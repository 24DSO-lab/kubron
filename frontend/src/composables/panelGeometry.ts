// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Геометрия плавающей панели: изменение размера за любую сторону или угол.
 * Чистые функции без DOM — поведение проверяется юнит-тестами.
 */

export interface PanelRect { x: number; y: number; width: number; height: number }
export interface PanelSize { width: number; height: number }

/** Сторона (n/s/e/w) или угол (ne/nw/se/sw), за которую тянут панель */
export type ResizeEdge = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw'

export const RESIZE_EDGES: readonly ResizeEdge[] = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']

export const PANEL_MIN_SIZE: PanelSize = { width: 400, height: 250 }

/**
 * Новый прямоугольник панели, когда указатель сместился на (dx, dy) от начала жеста.
 *
 * - Двигается только захваченная сторона, противоположная остаётся на месте — в том числе
 *   когда панель упёрлась в минимальный размер (иначе она «уезжала» бы вслед за курсором).
 * - Захваченная сторона не выходит за границы окна. Если панель уже частично за окном
 *   (её можно утащить за заголовок), эту сторону нельзя лишь сдвинуть ещё дальше наружу.
 * - Минимальный размер приоритетнее границ окна: в очень маленьком окне панель не схлопывается.
 */
export function resizeRect(
  start: PanelRect,
  edge: ResizeEdge,
  dx: number,
  dy: number,
  viewport: PanelSize,
  min: PanelSize = PANEL_MIN_SIZE,
): PanelRect {
  let left = start.x
  let top = start.y
  let right = start.x + start.width
  let bottom = start.y + start.height

  if (edge.includes('w')) {
    const limit = Math.min(0, start.x)
    left = Math.min(Math.max(start.x + dx, limit), right - min.width)
  }
  if (edge.includes('e')) {
    const limit = Math.max(viewport.width, right)
    right = Math.max(Math.min(right + dx, limit), left + min.width)
  }
  if (edge.includes('n')) {
    const limit = Math.min(0, start.y)
    top = Math.min(Math.max(start.y + dy, limit), bottom - min.height)
  }
  if (edge.includes('s')) {
    const limit = Math.max(viewport.height, bottom)
    bottom = Math.max(Math.min(bottom + dy, limit), top + min.height)
  }

  // Целые пиксели: на HiDPI clientX/clientY дробные, и границы панели «плыли» бы.
  const x = Math.round(left)
  const y = Math.round(top)
  return { x, y, width: Math.round(right) - x, height: Math.round(bottom) - y }
}
