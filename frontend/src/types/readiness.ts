// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Типы Upgrade Readiness (ADR-0025): зеркало JSON Go-пакета internal/readiness. Wails описывает структуры Go
 * классами, поэтому ответы RPC приводятся к этим интерфейсам в platform/readiness.ts.
 */

export type Severity = 'blocker' | 'warning' | 'info'
export type FindingType = 'api' | 'component' | 'platform' | 'source'
export type SourceKind = 'live_api' | 'gitops_manifests' | 'local_files'
export type ExportFormat = 'json' | 'markdown'

/** Статус finding: статусы API, четыре статуса компонентов (ADR-0025 §4) и служебные */
export type Status =
  | 'deprecated' | 'removed'
  | 'confirmed' | 'known_limitation' | 'insufficient_data' | 'not_recognized'
  | 'ok' | 'attention' | 'not_checked' | 'partial'

/** Пояснение: код и параметры; подпись по коду переводит интерфейс (readiness.codes.*) */
export interface Message {
  code: string
  params?: Record<string, string>
}

export interface ResourceRef {
  namespace?: string
  name: string
  evidence: string[]
}

export interface APIDetail {
  apiVersion: string
  kind: string
  deprecatedIn?: string
  removedIn?: string
  replacement?: string
  resources?: ResourceRef[]
  resourceCount: number
  resourcesTruncated?: boolean
  guideUrl?: string
}

export interface ComponentDetail {
  id?: string
  name?: string
  version?: string
  kind?: string
  namespace?: string
  workload?: string
  image?: string
  nodeCount?: number
  state?: 'ready' | 'degraded' | 'unavailable' | 'unknown'
  ready?: number
  desired?: number
  targetVersion?: string
  docsUrl?: string
  checkedAt?: string
  noteCode?: string
  note?: string
  noteUrl?: string
}

export interface Finding {
  key: string
  type: FindingType
  status: Status
  severity: Severity
  priority: number
  explanation: Message
  recommendation?: Message
  source: SourceKind
  observedAt: string
  api?: APIDetail
  component?: ComponentDetail
}

export interface Action {
  phase: 'before' | 'after'
  priority: number
  message: Message
  related?: string[]
  url?: string
}

export interface SourceCoverage {
  kind: SourceKind
  checked: boolean
  partial?: boolean
  observedAt?: string
  methods?: string[]
  scanned?: string[]
  notCovered?: string[]
  limitations?: Message[]
  reason?: Message
}

export interface MatrixInfo {
  version: string
  generatedAt: string
  window: string[]
  since: string
  source: string
  entries: number
}

export interface CatalogInfo {
  version: string
  checkedAt: string
  oldestCheck: string
  entries: number
}

export interface Coverage {
  mode: 'generic_kubernetes'
  targetSpecified: boolean
  evaluatedAgainst: string
  sources: SourceCoverage[]
  apiMatrix: MatrixInfo
  catalog: CatalogInfo
  limitations: Message[]
}

export interface Report {
  schemaVersion: number
  id: string
  clusterId: string
  createdAt: string
  initiator?: string
  correlationId?: string
  current: { gitVersion: string; minor: string; platform?: string }
  target?: string
  findings: Finding[]
  actions: Action[]
  coverage: Coverage
}

export interface Summary {
  id: string
  clusterId: string
  createdAt: string
  initiator?: string
  currentVersion: string
  targetVersion?: string
  blockers: number
  warnings: number
  infos: number
  notChecked: number
}

export interface HistoryPage {
  items: Summary[]
  next?: string
}

export interface FindingChange {
  key: string
  before: Finding
  after: Finding
  severity?: boolean
  status?: boolean
  version?: boolean
  fields?: string[]
}

export interface Diff {
  previousId: string
  currentId: string
  currentVersionBefore: string
  currentVersionAfter: string
  targetBefore?: string
  targetAfter?: string
  targetChanged: boolean
  matrixBefore: string
  matrixAfter: string
  matrixChanged: boolean
  catalogBefore: string
  catalogAfter: string
  catalogChanged: boolean
  new: Finding[]
  resolved: Finding[]
  changed: FindingChange[]
  unchanged: number
}

export interface ReadinessInfo {
  window: string[]
  apiMatrix: MatrixInfo
  catalog: CatalogInfo
}

export interface ExportedReport {
  format: ExportFormat
  filename: string
  mime: string
  content: string
}
