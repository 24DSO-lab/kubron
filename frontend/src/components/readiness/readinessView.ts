// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Чистые функции экрана Upgrade Readiness: группировка находок, подписи сообщений, сводки. Без обращений к бэкенду. */
import { te, t } from '../../i18n'
import type { Coverage, Diff, Finding, Message, Severity, SourceCoverage, Status } from '../../types/readiness'

export const SEVERITIES: readonly Severity[] = ['blocker', 'warning', 'info']

/** Подпись сообщения отчёта по коду; неизвестный интерфейсу код показывается как есть, а не пустым */
export function messageText(m: Message | undefined | null): string {
  if (!m) return ''
  const key = `readiness.codes.${m.code}`
  return te(key) ? t(key, { ...(m.params ?? {}) }) : m.code
}

/** Пояснение вердикта компонента: перевод по коду, иначе английская заметка каталога, иначе сам код */
export function noteText(code: string | undefined, note: string | undefined): string {
  if (code && te(`readiness.codes.${code}`)) return t(`readiness.codes.${code}`)
  return note || code || ''
}

/** Находки по важности; порядок внутри группы — по приоритету, назначенному бэкендом */
export function groupFindings(findings: readonly Finding[]): Record<Severity, Finding[]> {
  const out: Record<Severity, Finding[]> = { blocker: [], warning: [], info: [] }
  for (const f of findings) out[f.severity]?.push(f)
  for (const s of SEVERITIES) out[s].sort((a, b) => a.priority - b.priority)
  return out
}

/** «Не проверено» — отдельно от «нет проблем»: источник недоступен, запрещён или прочитан не целиком */
export function isNotChecked(status: Status): boolean {
  return status === 'not_checked' || status === 'partial'
}

export function notCheckedFindings(findings: readonly Finding[]): Finding[] {
  return findings.filter(f => isNotChecked(f.status))
}

/** Источники, которые отчёт не проверял вовсе (GitOps, локальные файлы) */
export function uncheckedSources(c: Coverage): SourceCoverage[] {
  return c.sources.filter(s => !s.checked)
}

/** Источники с неполной проверкой (проверены частично) */
export function partialSources(c: Coverage): SourceCoverage[] {
  return c.sources.filter(s => s.checked && s.partial)
}

/** Заголовок находки: «Kind apiVersion» для API, имя компонента, иначе ключ */
export function findingTitle(f: Finding): string {
  if (f.api) return `${f.api.kind} (${f.api.apiVersion})`
  if (f.component?.name) return f.component.name + (f.component.workload && f.component.workload !== f.component.name ? ` · ${f.component.workload}` : '')
  return f.key
}

export function resourceLabel(namespace: string | undefined, name: string): string {
  return namespace ? `${namespace}/${name}` : name
}

/** В сравнении что-то изменилось (иначе показываем «без изменений») */
export function diffHasChanges(d: Diff): boolean {
  return d.new.length + d.resolved.length + d.changed.length > 0 || d.targetChanged || d.matrixChanged || d.catalogChanged
}

/** Только http(s): ссылки в отчёте приходят из данных, схему проверяем перед открытием */
export function safeUrl(u: string | undefined): string {
  if (!u) return ''
  try {
    const p = new URL(u)
    return p.protocol === 'https:' || p.protocol === 'http:' ? p.toString() : ''
  } catch {
    return ''
  }
}
