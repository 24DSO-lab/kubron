// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Цвета и css-классы статусов: одно место для списков и панелей */

const OK = ['synced', 'healthy', 'running', 'ready', 'active', 'bound', 'succeeded', 'completed', 'complete', 'available', 'true', 'deployed', 'normal']
const WARN = ['pending', 'containercreating', 'podinitializing', 'terminating', 'progressing', 'pending-install', 'pending-upgrade', 'pending-rollback', 'pending-delete', 'uninstalling', 'superseded', 'warning', 'released', 'outofsync', 'suspended']
const ERR = [
  'failed', 'error', 'crashloopbackoff', 'crashed', 'imagepullbackoff', 'errimagepull', 'evicted',
  'lost', 'false', 'replicafailure', 'progressdeadlineexceeded',
  'degraded', 'missing',
]

/** Проверяет, является ли статус "N/N Running" — полный, но с числами */
function isFullNumericRunning(status?: string): boolean {
  if (!status) return false
  const match = status.match(/^(\d+)\/(\d+)\s*(running|ready)$/i)
  return !!match && match[1] === match[2]
}

/** Проверяет, является ли статус частичным (с числами) типа "1/3 Running" */
function isPartialRunning(status?: string): boolean {
  if (!status) return false
  const match = status.match(/^(\d+)\/(\d+)\s*(running|progressing|ready)$/i)
  if (!match) return false
  // N/N — полный статус, не частичный
  if (match[1] === match[2]) return false
  return true
}

/** Проверяет, является ли статус полным (без чисел) типа "Running" */
function isFullStatus(status?: string): boolean {
  if (!status) return false
  return /^(running|progressing|ready)$/i.test(status)
}

/** css-класс бейджа (стили в components/resource/resource.css) */
export function badgeClass(status?: string): string {
  // Полный статус Running -> OK (зелёный)
  if (isFullStatus(status)) {
    const s = status!.toLowerCase()
    if (OK.includes(s)) return 'rd-ok'
    if (WARN.includes(s)) return 'rd-warn'
    if (ERR.includes(s)) return 'rd-err'
    return 'rd-unk'
  }
  // N/N Running → OK (зелёный)
  if (isFullNumericRunning(status)) return 'rd-ok'
  // Частичный статус (с числами) -> WARN (жёлтый)
  if (isPartialRunning(status)) return 'rd-warn'
  const s = (status ?? '').toLowerCase()
  if (OK.includes(s)) return 'rd-ok'
  if (WARN.includes(s)) return 'rd-warn'
  if (ERR.includes(s)) return 'rd-err'
  return 'rd-unk'
}

/** Цвет текста для колонок списка (colorFn) */
export function statusColor(status?: string): string {
  switch (badgeClass(status)) {
    case 'rd-ok': return 'var(--green)'
    case 'rd-warn': return 'var(--yellow)'
    case 'rd-err': return 'var(--red)'
    default: return 'var(--text-gray)'
  }
}
