// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Типы Kubron Intelligence (ADR-0024): повторяют DTO internal/diagnose и internal/incident.
 * Факт, гипотеза, рекомендация и шаг — РАЗНЫЕ типы с различающим полем `kind`: интерфейс не смешивает их.
 * Среди значений уверенности нет «установлено»: гипотезу подтверждает только человек (Outcome).
 */

export type ReportKind = 'fact' | 'hypothesis' | 'recommendation' | 'step'
export type Confidence = 'low' | 'medium' | 'high'
export type SourceKind = 'resource' | 'event' | 'log' | 'derived'
export type IncidentStatus = 'open' | 'closed'
export type OutcomeResult = 'resolved' | 'unresolved' | 'inconclusive'
export type EvidenceStatus = 'saved' | 'not_saved'
export type WorkloadKind = 'Pod' | 'Deployment' | 'ReplicaSet'

export type Params = Record<string, string>

export interface ResourceRef {
  kind: string
  namespace?: string
  name: string
  uid?: string
}

export interface Source {
  kind: SourceKind
  resource: ResourceRef
  field?: string
}

export interface LogRef {
  container: string
  previous?: boolean
}

export interface Fact {
  kind: 'fact'
  id: string
  code: string
  resource: ResourceRef
  params?: Params
  source: Source
  observedAt: string
  log?: LogRef
}

export interface Hypothesis {
  kind: 'hypothesis'
  id: string
  code: string
  resource: ResourceRef
  params?: Params
  confidence: Confidence
  supporting: string[]
  contradicting: string[]
}

export interface Recommendation {
  kind: 'recommendation'
  id: string
  code: string
  resource: ResourceRef
  params?: Params
  hypotheses: string[]
}

export interface Step {
  kind: 'step'
  id: string
  code: string
  resource: ResourceRef
  params?: Params
  hypotheses: string[]
}

export interface MissingContext {
  code: string
  resource: ResourceRef
  params?: Params
}

export interface Report {
  ruleSetVersion: number
  target: ResourceRef
  observedAt: string
  symptoms: string[]
  resources: ResourceRef[]
  facts: Fact[]
  hypotheses: Hypothesis[]
  recommendations: Recommendation[]
  steps: Step[]
  missing: MissingContext[]
  partial?: string[]
}

export interface IncidentEvent {
  type: string
  reason: string
  message: string
  count: number
  firstSeen: string
  lastSeen: string
  object: ResourceRef
  component?: string
}

export interface Evidence {
  pod: string
  container: string
  previous?: boolean
  status: EvidenceStatus
  reason?: string
  fragmentId?: string
  updatedAt: string
}

export interface Outcome {
  result: OutcomeResult
  confirmedHypothesisId?: string
  note?: string
  markedBy: string
  markedAt: string
}

export interface Incident {
  id: string
  target: ResourceRef
  status: IncidentStatus
  openedAt: string
  updatedAt: string
  openedBy: string
  report: Report
  events: IncidentEvent[]
  eventsUpdatedAt: string
  saveLogs: boolean
  evidence: Evidence[]
  outcome?: Outcome
  version: number
}

export interface FragmentMeta {
  id: string
  incidentId: string
  namespace: string
  pod: string
  container: string
  previous?: boolean
  capturedAt: string
  fromTime?: string
  toTime?: string
  startLine: number
  endLine: number
  anchorLine: number
  lines: number
  bytes: number
  truncated: boolean
  redactorVersion: number
  replacements: number
  sha256: string
  auto: boolean
  createdAt: string
  createdBy: string
}

export interface FragmentView extends FragmentMeta {
  /** У пользователя нет права diagnostics.logs: метаданные видны, содержимое скрыто */
  contentHidden: boolean
}

export interface Fragment extends FragmentMeta {
  text: string
}

export interface IncidentDetail {
  incident: Incident
  fragments: FragmentView[]
}

export interface IncidentSummary {
  id: string
  target: ResourceRef
  status: IncidentStatus
  openedAt: string
  updatedAt: string
  openedBy: string
  symptoms: string[]
  facts: number
  hypotheses: number
  result?: OutcomeResult
}

export interface IncidentPage {
  items: IncidentSummary[]
  nextCursor?: string
}

export interface IncidentSettings {
  disableLogEvidence: boolean
  excludedNamespaces: string[]
  fragmentRetentionDays: number
  incidentRetentionDays: number
}

export interface IncidentLimits {
  fragmentLines: number
  fragmentBytes: number
  lineBytes: number
  fragmentsPerIncident: number
  incidentFragmentBytes: number
  maxFragmentRetentionDays: number
  maxIncidentRetentionDays: number
}

export interface IncidentSettingsInfo {
  settings: IncidentSettings
  limits: IncidentLimits
  canManage: boolean
}

export interface OpenIncidentRequest {
  kind: WorkloadKind
  namespace: string
  name: string
  disableLogs: boolean
}
