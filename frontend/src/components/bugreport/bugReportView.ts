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

/** Куда пойдёт отчёт: подпись в диалоге выбирается по этому значению. 'none' — отправлять некуда и не сохранить. */
export type DeliveryHint = 'mail' | 'relay' | 'queue' | 'none'

export function deliveryHint(status: BugReportStatus | null): DeliveryHint {
  if (!status) return 'none'
  if (status.channel === 'mail') return 'mail'
  if (status.channel === 'relay') return 'relay'
  return status.canQueue ? 'queue' : 'none'
}

/** Отправить можно, если есть способ доставки (или очередь, где отчёт подождёт), описание не пусто и не длиннее предела */
export function canSendReport(description: string, status: BugReportStatus | null, busy: boolean): boolean {
  const len = [...description.trim()].length
  return !busy && deliveryHint(status) !== 'none' && len > 0 && len <= MAX_DESCRIPTION
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
