// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Типы baseline конфигурации и результатов проверки (ADR-0027). Повторяют JSON бэкенда (internal/baseline, internal/drift). */

export type BaselineStatus = 'match' | 'changed' | 'missing' | 'unchecked' | 'new'
export type BaselinePriority = 'high' | 'medium' | 'low'
export type BaselineChangeOp = 'changed' | 'added' | 'removed'

/** Причина статуса «не проверено» (и пропуска при снимке) */
export type BaselineReason =
  | 'forbidden' | 'unavailable' | 'too-large' | 'kind-unavailable' | 'rules-version-mismatch'
  | 'secret-unsupported' | 'unsupported-kind' | 'truncated' | 'not-found'

export interface BaselineRef {
  group?: string
  version: string
  kind: string
  namespace?: string
  name: string
}

export interface Baseline {
  id: string
  name: string
  namespaces: string[]
  selector: string
  kinds: string[]
  refs: BaselineRef[]
  activeVersionId: string
  createdBy: string
  createdAt: string
}

/** Что снимать: пространства имён + селектор + типы либо явный список ресурсов */
export interface BaselineSpec {
  namespaces: string[]
  selector: string
  kinds: string[]
  refs: BaselineRef[]
}

export interface BaselineVersionSummary {
  id: string
  baselineId: string
  seq: number
  rulesVersion: number
  source: string
  author: string
  comment: string
  createdAt: string
  objectCount: number
}

export interface BaselineVersionPage { items: BaselineVersionSummary[]; next?: string }

export interface BaselineChangeSource {
  kind: 'managedFields' | 'kubron-audit'
  manager?: string
  at: string
}

export interface BaselineChange {
  path: string
  op: BaselineChangeOp
  /** Значения для показа. У чувствительных изменений пусты: бэкенд их не отдаёт */
  old?: string
  new?: string
  sensitive?: boolean
  key?: string
  priority: BaselinePriority
  source?: BaselineChangeSource
}

export interface BaselineItem {
  ref: BaselineRef
  status: BaselineStatus
  reason?: BaselineReason
  priority?: BaselinePriority
  changes?: BaselineChange[]
  suppressed?: string[]
}

export interface BaselineSummary {
  total: number
  matched: number
  changed: number
  missing: number
  unchecked: number
  checked: number
  uncheckedByReason?: Partial<Record<BaselineReason, number>>
  suppressedChanges: number
}

export interface BaselineException {
  id: string
  scope: 'resource' | 'kind'
  ref: BaselineRef
  path: string
  comment: string
  expiresAt?: string
  createdBy: string
  createdAt: string
}

export interface BaselineAppliedException {
  id: string
  scope: 'resource' | 'kind'
  ref: BaselineRef
  path: string
  comment: string
  expiresAt?: string
  hits: number
}

export interface BaselineCheckResult {
  id: string
  baselineId: string
  versionId: string
  rulesVersion: number
  versionRulesVersion: number
  checkedAt: string
  initiator: string
  summary: BaselineSummary
  items: BaselineItem[]
  exceptions: { applied?: BaselineAppliedException[]; expired?: string[] }
  caveats?: string[]
}

export interface BaselineResultRow {
  id: string
  baselineId: string
  versionId: string
  checkedAt: string
  initiator: string
  rulesVersion: number
  summary: BaselineSummary
}

export interface BaselineResultPage { items: BaselineResultRow[]; next?: string }

export interface BaselineSkip { ref: BaselineRef; reason: BaselineReason }

export interface BaselinePreview {
  token: string
  expiresAt: string
  objects: number
  hashed: number
  skipped?: BaselineSkip[]
  hasPrevious: boolean
  rulesChanged: boolean
  added?: BaselineRef[]
  removed?: BaselineRef[]
  changed?: BaselineItem[]
  unchanged: number
}
