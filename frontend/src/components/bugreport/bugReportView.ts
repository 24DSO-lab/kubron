// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { BugReportStatus, MailSecurity } from '../../types/bugreport'

/** Те же границы, что у bugreport.MaxDescriptionRunes */
export const MAX_DESCRIPTION = 5000

/** Самая длинная сторона снимка, пикселей: письмо не должно разрастаться на больших и retina-экранах */
export const SCREENSHOT_MAX_SIDE = 2400

/** Масштаб рендера: до 2× (retina), но так, чтобы длинная сторона не превысила SCREENSHOT_MAX_SIDE */
export function screenshotScale(width: number, height: number, dpr: number): number {
  const side = Math.max(width, height, 1)
  const wanted = Math.min(Math.max(dpr || 1, 1), 2)
  return Math.min(wanted, SCREENSHOT_MAX_SIDE / side)
}

/** `data:image/png;base64,AAAA` → `AAAA`; не PNG — пустая строка */
export function pngBase64(dataUrl: string): string {
  const prefix = 'data:image/png;base64,'
  return dataUrl.startsWith(prefix) ? dataUrl.slice(prefix.length) : ''
}

/** Можно ли открыть почтовую программу: нужна поддержка оболочки и непустое описание допустимой длины */
export function canOpenMailClient(description: string, status: BugReportStatus | null, busy: boolean): boolean {
  const len = [...description.trim()].length
  return !busy && !!status?.canDraft && len > 0 && len <= MAX_DESCRIPTION
}

/** Сочетание клавиш вставки для подсказки: ⌘V на macOS, Ctrl+V в остальных системах */
export function pasteShortcut(platform: string): string {
  return /mac|iphone|ipad/i.test(platform) ? '⌘V' : 'Ctrl+V'
}

/** PNG из data URL в Blob без fetch: CSP может не разрешать data: в connect-src. Не PNG — null. */
export function pngBlob(dataUrl: string): Blob | null {
  const b64 = pngBase64(dataUrl)
  if (!b64) return null
  try {
    const bin = atob(b64)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new Blob([bytes], { type: 'image/png' })
  } catch {
    return null
  }
}

export function canSendReport(description: string, status: BugReportStatus | null, busy: boolean): boolean {
  const len = [...description.trim()].length
  return !busy && !!status?.configured && len > 0 && len <= MAX_DESCRIPTION
}

/** Порт по умолчанию для способа защиты соединения (как в bugreport.SMTPConfig.Normalize) */
export function defaultPort(security: MailSecurity): number {
  return security === 'tls' ? 465 : security === 'none' ? 25 : 587
}

export function canSaveMailSettings(host: string, port: number, username: string, password: string, passwordKept: boolean): boolean {
  if (!host.trim() || !Number.isInteger(port) || port < 1 || port > 65535) return false
  return !username.trim() || !!password || passwordKept
}

/**
 * Предел снимка в base64 (символов). Веб-сервер принимает тело запроса до 4 МиБ (maxRPCBody), в нём ещё описание,
 * поэтому снимок ужимается заранее, а не отклоняется сервером.
 */
export const SCREENSHOT_MAX_BASE64 = 3_400_000

/** Во сколько раз уменьшать масштаб при каждой новой попытке */
const SHRINK = 0.7
const MAX_ATTEMPTS = 5

/**
 * Рисует снимок, пока он не уложится в предел: масштаб каждый раз уменьшается. Если и после MAX_ATTEMPTS попыток
 * снимок слишком большой — ошибка, и отчёт уходит без него.
 */
export async function renderWithinLimit(render: (scale: number) => Promise<string>, scale: number): Promise<string> {
  let current = scale
  for (let i = 0; i < MAX_ATTEMPTS; i++) {
    const url = await render(current)
    if (pngBase64(url).length > 0 && pngBase64(url).length <= SCREENSHOT_MAX_BASE64) return url
    current *= SHRINK
  }
  throw new Error('screenshot is too large')
}
