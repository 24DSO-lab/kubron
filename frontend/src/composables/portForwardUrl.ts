// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { callApp } from '../platform/api'
import { useCapabilities } from './useCapabilities'

/** Проброс идёт через прокси сервера (адрес одноразовый), а не на localhost браузера. */
export function isProxyForward(session?: { mode?: string } | null): boolean {
  return (session?.mode ?? useCapabilities().capabilities.value.portForwardMode) === 'proxy'
}

/**
 * Адрес для браузера. Его всегда даёт бэкенд: в режиме proxy он одноразовый и живёт секунды, поэтому
 * запрашивается при каждом открытии, а не хранится и не собирается из localPort.
 * secure — открыть по https (локальный проброс на TLS-порт; режим proxy выбирает схему сам).
 */
export async function getPortForwardUrl(id: string, secure = false): Promise<string> {
  const url: string = await callApp('GetPortForwardURL', id)
  return secure && url.startsWith('http://') ? 'https://' + url.slice('http://'.length) : url
}
