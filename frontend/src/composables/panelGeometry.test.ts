// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { PANEL_MIN_SIZE, RESIZE_EDGES, resizeRect, type PanelRect, type ResizeEdge } from './panelGeometry'

const viewport = { width: 1600, height: 900 }
// Панель с запасом по всем сторонам: left 300, right 1000, top 200, bottom 700
const start: PanelRect = { x: 300, y: 200, width: 700, height: 500 }

describe('resizeRect: одна сторона', () => {
  it('e: тянет правую сторону, левый край на месте', () => {
    expect(resizeRect(start, 'e', 100, 50, viewport)).toEqual({ x: 300, y: 200, width: 800, height: 500 })
  })
  it('w: тянет левую сторону, правый край на месте', () => {
    expect(resizeRect(start, 'w', -100, 50, viewport)).toEqual({ x: 200, y: 200, width: 800, height: 500 })
  })
  it('s: тянет нижнюю сторону, верхний край на месте', () => {
    expect(resizeRect(start, 's', 50, 100, viewport)).toEqual({ x: 300, y: 200, width: 700, height: 600 })
  })
  it('n: тянет верхнюю сторону, нижний край на месте', () => {
    expect(resizeRect(start, 'n', 50, -100, viewport)).toEqual({ x: 300, y: 100, width: 700, height: 600 })
  })
})

describe('resizeRect: углы', () => {
  it('se меняет ширину и высоту, как прежний хендл', () => {
    expect(resizeRect(start, 'se', 40, 30, viewport)).toEqual({ x: 300, y: 200, width: 740, height: 530 })
  })
  it('nw двигает левый верхний угол, правый нижний неподвижен', () => {
    expect(resizeRect(start, 'nw', -40, -30, viewport)).toEqual({ x: 260, y: 170, width: 740, height: 530 })
  })
  it('ne', () => {
    expect(resizeRect(start, 'ne', 40, -30, viewport)).toEqual({ x: 300, y: 170, width: 740, height: 530 })
  })
  it('sw', () => {
    expect(resizeRect(start, 'sw', -40, 30, viewport)).toEqual({ x: 260, y: 200, width: 740, height: 530 })
  })
})

describe('resizeRect: минимальный размер', () => {
  it('w: упёршись в минимум, правый край не уезжает', () => {
    const r = resizeRect(start, 'w', 5000, 0, viewport)
    expect(r.width).toBe(PANEL_MIN_SIZE.width)
    expect(r.x + r.width).toBe(1000)
  })
  it('n: упёршись в минимум, нижний край не уезжает', () => {
    const r = resizeRect(start, 'n', 0, 5000, viewport)
    expect(r.height).toBe(PANEL_MIN_SIZE.height)
    expect(r.y + r.height).toBe(700)
  })
  it('e и s не уже минимума, левый верхний угол на месте', () => {
    const r = resizeRect(start, 'se', -5000, -5000, viewport)
    expect(r).toEqual({ x: 300, y: 200, ...PANEL_MIN_SIZE })
  })
  it('минимум приоритетнее границ окна', () => {
    const tiny = { width: 300, height: 200 }
    const r = resizeRect({ x: 0, y: 0, width: 500, height: 300 }, 'se', -400, -400, tiny)
    expect(r).toEqual({ x: 0, y: 0, ...PANEL_MIN_SIZE })
  })
})

describe('resizeRect: границы окна', () => {
  it('w не выходит за левый край окна', () => {
    expect(resizeRect(start, 'w', -5000, 0, viewport)).toEqual({ x: 0, y: 200, width: 1000, height: 500 })
  })
  it('n не выходит за верхний край окна', () => {
    expect(resizeRect(start, 'n', 0, -5000, viewport)).toEqual({ x: 300, y: 0, width: 700, height: 700 })
  })
  it('e не выходит за правый край окна', () => {
    expect(resizeRect(start, 'e', 5000, 0, viewport)).toEqual({ x: 300, y: 200, width: 1300, height: 500 })
  })
  it('s не выходит за нижний край окна', () => {
    expect(resizeRect(start, 's', 0, 5000, viewport)).toEqual({ x: 300, y: 200, width: 700, height: 700 })
  })
  it('панель, частично вынесенная за окно, не «прыгает» при захвате другой стороны', () => {
    const offscreen: PanelRect = { x: -500, y: 200, width: 700, height: 500 }
    expect(resizeRect(offscreen, 'e', 10, 0, viewport)).toEqual({ x: -500, y: 200, width: 710, height: 500 })
  })
  it('вынесенную за окно сторону нельзя сдвинуть ещё дальше наружу', () => {
    const offscreen: PanelRect = { x: -500, y: 200, width: 700, height: 500 }
    expect(resizeRect(offscreen, 'w', -100, 0, viewport).x).toBe(-500)
    // но внутрь — можно
    expect(resizeRect(offscreen, 'w', 100, 0, viewport).x).toBe(-400)
  })
})

describe('resizeRect: прочее', () => {
  it('нулевое смещение ничего не меняет для любой стороны', () => {
    for (const edge of RESIZE_EDGES) expect(resizeRect(start, edge, 0, 0, viewport)).toEqual(start)
  })
  it('дробное смещение округляется до целых пикселей', () => {
    expect(resizeRect(start, 'se', 10.6, 10.4, viewport)).toEqual({ x: 300, y: 200, width: 711, height: 510 })
  })
  it('не мутирует исходный прямоугольник', () => {
    const copy = { ...start }
    resizeRect(start, 'nw', -40, -40, viewport)
    expect(start).toEqual(copy)
  })
  it('вертикальная сторона не трогает ширину, горизонтальная — высоту', () => {
    const vertical: ResizeEdge[] = ['n', 's']
    for (const edge of vertical) {
      const r = resizeRect(start, edge, 123, 40, viewport)
      expect(r.x).toBe(start.x)
      expect(r.width).toBe(start.width)
    }
    const horizontal: ResizeEdge[] = ['e', 'w']
    for (const edge of horizontal) {
      const r = resizeRect(start, edge, 40, 123, viewport)
      expect(r.y).toBe(start.y)
      expect(r.height).toBe(start.height)
    }
  })
})
