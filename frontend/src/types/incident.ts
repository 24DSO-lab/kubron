// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Типы Kubron Intelligence (ADR-0024): повторяют DTO internal/diagnose и internal/incident.
 * Факт, гипотеза, рекомендация и шаг — РАЗНЫЕ типы с различающим полем `kind`: интерфейс не смешивает их.
 * Среди значений уверенности нет «установлено»: гипотезу подтверждает только человек (Outcome).
 */

export type ReportKind = 'fact' | 'hypothesis' | 'recommendation' | 'step'
export type Confidence = 'low' | 'medium' | 'high'
export type SourceKind = 'resource' | 'event' | 'log' | 'derived' | 'metric'
export type IncidentStatus = 'open' | 'closed'
export type OutcomeResult = 'resolved' | 'unresolved' | 'inconclusive'
export type EvidenceStatus = 'saved' | 'not_saved'
export type WorkloadKind = 'Pod' | 'Deployment' | 'ReplicaSet'

export type Params = Record<string, string>

export interface ResourceRef {
  group?: string
  kind: string
  namespace?: string
  name: string
  uid?: string
}

export interface Source {
  kind: SourceKind
  resource: ResourceRef
  field?: string
  /** Сигнал метрики (SourceKind = 'metric'), ADR-0032 */
  signal?: string
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
  ruleId?: string
  ruleEpoch?: number
  /** ID гипотез, которые объясняет эта (ADR-0031): она — кандидат первопричины */
  explains?: string[]
}

export interface Recommendation {
  kind: 'recommendation'
  id: string
  code: string
  resource: ResourceRef
  params?: Params
  hypotheses: string[]
  ruleId?: string
  ruleEpoch?: number
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

/** Снимок графа зависимостей (internal/depgraph.Snapshot). */
export type GraphHealth = 'Healthy' | 'Degraded' | 'Progressing' | 'Suspended' | 'Missing' | 'Unknown'
export type GraphUnknownReason = 'forbidden' | 'unavailable' | 'too_large' | 'limit' | 'policy'

export interface GraphNode {
  group?: string
  kind: string
  namespace?: string
  name: string
  health: GraphHealth | string
  reason?: string
  unknown?: GraphUnknownReason | string
  placeholder?: boolean
}

export interface GraphEdge {
  /** Ref.Key(): «group/kind/namespace/name» */
  from: string
  to: string
  type: string
  state: string
  reason?: string
}

export interface GraphSnapshot {
  nodes: GraphNode[]
  edges: GraphEdge[]
  truncated?: boolean
  warnings?: string[]
}

export interface ChainStep {
  ref: ResourceRef
  health: GraphHealth | string
  reason?: string
}

export interface Chain {
  path: ChainStep[]
  /** ID гипотезы-кандидата первопричины */
  candidate?: string
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
  graph?: GraphSnapshot
  chains?: Chain[]
}

/** Корреляция (internal/correlation, ADR-0033) */
export type CorrelationKind = 'shared_node' | 'shared_storage' | 'shared_dependency' | 'shared_root' | 'shared_symptom'

export interface WorkloadRef {
  kind: string
  namespace?: string
  name: string
  problemPods?: number
  symptoms?: string[]
  since?: string
}

export interface CorrelationCandidate {
  id: string
  kind: CorrelationKind | string
  source: ResourceRef
  confidence: Confidence
  since?: string
  affected?: WorkloadRef[]
  total: number
  truncated?: boolean
  potential?: WorkloadRef[]
  supporting?: string[]
}

export interface CorrelationResult {
  version: number
  observedAt: string
  candidates?: CorrelationCandidate[]
  facts?: Fact[]
  missing?: MissingContext[]
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

export type CorrelationVerdictValue = 'confirmed' | 'rejected' | 'unknown'

export interface CorrelationFeedback {
  candidateId: string
  verdict: CorrelationVerdictValue
}

export interface Outcome {
  result: OutcomeResult
  confirmedHypothesisId?: string
  rejectedHypothesisIds?: string[]
  helpfulRecommendationIds?: string[]
  uselessRecommendationIds?: string[]
  otherAction?: boolean
  resolvedAt?: string
  correlation?: CorrelationFeedback
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
  correlation?: CorrelationResult
  problemId?: string
  signature?: string
  recurrenceOf?: string
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
  problemId?: string
  signature?: string
  recurrenceOf?: string
  resolvedAt?: string
  ruleSetVersion?: number
  confirmed?: RuleRef
  rejected?: RuleRef[]
  helpful?: RuleRef[]
  useless?: RuleRef[]
  features?: string[]
  correlationVerdict?: { kind: string; confidence: string; verdict: string }
}

/** Ключ статистики правил: (ruleId, epoch, code) */
export interface RuleRef {
  ruleId: string
  epoch: number
  code: string
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

/** Вход IncidentMarkOutcome. Обязателен только result; не заданное поле = «не отмечено». */
export interface OutcomeInput {
  result: OutcomeResult
  confirmedHypothesisId?: string
  note?: string
  rejectedHypothesisIds?: string[]
  helpfulRecommendationIds?: string[]
  uselessRecommendationIds?: string[]
  otherAction?: boolean
  resolvedAt?: string
  correlation?: CorrelationFeedback
}

/** Проблемы (ADR-0033, contracts.md §7) */
export type ProblemStatus = 'open' | 'closed'
export type ProblemOrigin = 'auto' | 'manual'

export interface ProblemSummary {
  id: string
  key: string
  kind: CorrelationKind | string
  source: ResourceRef
  status: ProblemStatus
  origin: ProblemOrigin
  memberCount: number
  affectedTotal: number
  updatedAt: string
}

export interface ProblemMemberView {
  incidentId: string
  addedAt: string
  /** ID пользователя или «auto» */
  addedBy: string
  summary?: IncidentSummary
}

export interface ProblemDetail extends ProblemSummary {
  members: ProblemMemberView[]
  affected?: WorkloadRef[]
  affectedTruncated?: boolean
  detached?: string[]
}

export interface ProblemPage {
  items: ProblemSummary[]
  nextCursor?: string
}

/** Статистика правил (internal/feedback.Stats, camelCase). Нет ключа доли = нет процента. */
export interface HypothesisStat extends RuleRef {
  confirmed: number
  rejected: number
  precision?: number
  sample: number
  trusted: boolean
  medianResolveSeconds?: number
  resolved: number
  recurred: number
}

export interface RecommendationStat extends RuleRef {
  helpful: number
  useless: number
  ratio?: number
  sample: number
  trusted: boolean
  previouslyUnhelpful?: boolean
}

export interface SymptomStat {
  code: string
  closed: number
  withConfirmed: number
  coverage?: number
}

export interface CorrelationStat {
  kind: string
  confidence: string
  confirmed: number
  rejected: number
  precision?: number
}

export interface FeatureCounts {
  records: number
  confirmed: number
  rejected: number
  precision?: number
}

export interface FeatureStat {
  feature: string
  with: FeatureCounts
  without: FeatureCounts
}

export interface RuleStats {
  records: number
  unevaluated: number
  truncated?: boolean
  computedAt: string
  ruleSetVersions: number[]
  hypotheses: HypothesisStat[]
  recommendations: RecommendationStat[]
  symptoms: SymptomStat[]
  correlations: CorrelationStat[]
  features: FeatureStat[]
}

export interface RuleStatsExport {
  filename: string
  mime: string
  dataBase64: string
}
