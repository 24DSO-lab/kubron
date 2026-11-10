// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'
import type { BugReportStatus } from '../../types/bugreport'
import {
  deliveryHint,
  MAX_DESCRIPTION, SCREENSHOT_MAX_BASE64, SCREENSHOT_MAX_SIDE, renderWithinLimit, canSaveMailSettings, canSendReport, defaultPort, pngBase64, screenshotScale,
} from './bugReportView'

const status = (configured: boolean, over: Partial<BugReportStatus> = {}): BugReportStatus => ({
  configured, source: configured ? 'saved' : '', recipient: 'a@b.c', canSave: true,
  channel: configured ? 'mail' : '', canQueue: false, pending: 0,
  settings: { host: '', port: 587, username: '', password: '', from: '', to: '', security: 'starttls', passwordSet: false },
  ...over,
})

describe('screenshotScale', () => {
  it('на retina — 2×, на обычном экране — 1×', () => {
    expect(screenshotScale(1200, 800, 3)).toBe(2)
    expect(screenshotScale(1200, 800, 1)).toBe(1)
    expect(screenshotScale(1200, 800, 0)).toBe(1)
  })
  it('большой экран уменьшает масштаб, чтобы длинная сторона не превысила предел', () => {
    const s = screenshotScale(3840, 2160, 2)
    expect(3840 * s).toBeLessThanOrEqual(SCREENSHOT_MAX_SIDE)
    expect(s).toBeLessThan(1)
  })
})

describe('pngBase64', () => {
  it('отрезает префикс data URL', () => {
    expect(pngBase64('data:image/png;base64,QUJD')).toBe('QUJD')
  })
  it('не PNG — пусто: в письмо уйти нечему', () => {
    expect(pngBase64('data:image/jpeg;base64,QUJD')).toBe('')
    expect(pngBase64('')).toBe('')
  })
})

describe('canSendReport', () => {
  it('нужны настроенная почта и непустое описание', () => {
    expect(canSendReport('broken', status(true), false)).toBe(true)
    expect(canSendReport('broken', status(false), false)).toBe(false)
    expect(canSendReport('broken', null, false)).toBe(false)
    expect(canSendReport('  \n ', status(true), false)).toBe(false)
  })
  it('во время отправки и при слишком длинном описании — нельзя', () => {
    expect(canSendReport('broken', status(true), true)).toBe(false)
    expect(canSendReport('я'.repeat(MAX_DESCRIPTION + 1), status(true), false)).toBe(false)
    expect(canSendReport('я'.repeat(MAX_DESCRIPTION), status(true), false)).toBe(true)
  })
})

describe('настройки почты', () => {
  it('порт по умолчанию зависит от защиты соединения', () => {
    expect(defaultPort('starttls')).toBe(587)
    expect(defaultPort('tls')).toBe(465)
    expect(defaultPort('none')).toBe(25)
  })
  it('сохранить можно с сервером и корректным портом; пользователю нужен пароль (новый или сохранённый)', () => {
    expect(canSaveMailSettings('smtp.example.com', 587, '', '', false)).toBe(true)
    expect(canSaveMailSettings('', 587, '', '', false)).toBe(false)
    expect(canSaveMailSettings('smtp.example.com', 0, '', '', false)).toBe(false)
    expect(canSaveMailSettings('smtp.example.com', 587, 'u', '', false)).toBe(false)
    expect(canSaveMailSettings('smtp.example.com', 587, 'u', 'pw', false)).toBe(true)
    expect(canSaveMailSettings('smtp.example.com', 587, 'u', '', true)).toBe(true)
  })
})

describe('renderWithinLimit', () => {
  const png = (n: number) => 'data:image/png;base64,' + 'A'.repeat(n)

  it('маленький снимок берётся с первой попытки', async () => {
    const render = vi.fn(async () => png(1000))
    expect(await renderWithinLimit(render, 2)).toBe(png(1000))
    expect(render).toHaveBeenCalledTimes(1)
    expect(render).toHaveBeenCalledWith(2)
  })

  it('слишком большой снимок рисуется заново в меньшем масштабе', async () => {
    const render = vi.fn(async (scale: number) => png(scale > 1.2 ? SCREENSHOT_MAX_BASE64 + 1 : 1000))
    const url = await renderWithinLimit(render, 2)
    expect(url).toBe(png(1000))
    const scales = render.mock.calls.map((c) => c[0])
    expect(scales).toHaveLength(3)
    scales.forEach((v, i) => expect(v).toBeCloseTo([2, 1.4, 0.98][i], 5))
  })

  it('если не помогает и уменьшение — ошибка, отчёт уходит без снимка', async () => {
    const render = vi.fn(async () => png(SCREENSHOT_MAX_BASE64 + 1))
    await expect(renderWithinLimit(render, 1)).rejects.toThrow()
    expect(render).toHaveBeenCalledTimes(5)
  })

  it('результат не PNG считается неудачей', async () => {
    await expect(renderWithinLimit(async () => 'data:text/html;base64,AAAA', 1)).rejects.toThrow()
  })
})

describe('способ доставки', () => {
  it('подсказка выбирается по каналу; без канала отчёт ждёт в очереди, а без очереди отправлять некуда', () => {
    expect(deliveryHint(null)).toBe('none')
    expect(deliveryHint(status(true))).toBe('mail')
    expect(deliveryHint(status(false, { channel: 'relay' }))).toBe('relay')
    expect(deliveryHint(status(false, { canQueue: true }))).toBe('queue')
    expect(deliveryHint(status(false))).toBe('none')
  })

  it('отправить можно при любом способе доставки, пока есть описание допустимой длины', () => {
    expect(canSendReport('broken', status(false, { channel: 'relay' }), false)).toBe(true)
    expect(canSendReport('broken', status(false, { canQueue: true }), false)).toBe(true)
    expect(canSendReport('broken', status(false), false)).toBe(false)
    expect(canSendReport('  ', status(true), false)).toBe(false)
    expect(canSendReport('broken', status(true), true)).toBe(false)
    expect(canSendReport('x'.repeat(MAX_DESCRIPTION + 1), status(true), false)).toBe(false)
    expect(canSendReport('broken', null, false)).toBe(false)
  })
})
