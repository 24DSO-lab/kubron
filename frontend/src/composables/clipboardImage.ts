// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { pngBlob } from '../components/bugreport/bugReportView'

/**
 * Кладёт PNG в буфер обмена как изображение. Вызывать синхронно из обработчика клика: браузерный API требует
 * действия пользователя, и после await оно может быть уже недействительно. Возвращает false, если веб-вью не
 * поддерживает запись изображений (например, старые WebKitGTK) или доступ запрещён; тогда остаётся файл.
 */
export function copyImageToClipboard(dataUrl: string): Promise<boolean> {
  try {
    const blob = pngBlob(dataUrl)
    if (!blob || !navigator.clipboard?.write || typeof ClipboardItem === 'undefined') return Promise.resolve(false)
    return navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]).then(() => true, () => false)
  } catch {
    return Promise.resolve(false)
  }
}
