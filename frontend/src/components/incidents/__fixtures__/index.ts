// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Типизированные фикстуры Kubron Intelligence (контракты — docs/prompts/contracts.md §3–7).
 * Интерфейс работает на них до появления реальных RPC (D7); типы проверяют их форму.
 */
import type {
  Chain, CorrelationResult, Fact, Incident, IncidentSummary, MissingContext, ProblemDetail, Report, ResourceRef,
  RuleStats, WorkloadRef,
} from '../../../types/incident'

const T = '2026-11-19T10:00:00Z'
const ref = (kind: string, name: string, namespace?: string, group = 'core'): ResourceRef => ({
  group, kind, name, ...(namespace ? { namespace } : {}),
})

export const podRef = ref('Pod', 'api-1', 'shop')
export const pvcRef = ref('PersistentVolumeClaim', 'data', 'shop')
export const scRef = ref('StorageClass', 'fast', undefined, 'storage.k8s.io')
export const nodeRef = ref('Node', 'n1')

const fact = (id: string, code: string, resource: ResourceRef, params?: Record<string, string>, signal?: string): Fact => ({
  kind: 'fact', id, code, resource, ...(params ? { params } : {}), observedAt: T,
  source: { kind: signal ? 'metric' : 'resource', resource, ...(signal ? { signal } : {}) },
})

const report = (extra: Partial<Report> = {}): Report => ({
  ruleSetVersion: 2, target: podRef, observedAt: T, symptoms: ['pod.pending'], resources: [podRef],
  facts: [], hypotheses: [], recommendations: [], steps: [], missing: [], ...extra,
})

const incident = (r: Report, extra: Partial<Incident> = {}): Incident => ({
  id: 'inc-1', target: podRef, status: 'open', openedAt: T, updatedAt: T, openedBy: 'alice', report: r,
  events: [], eventsUpdatedAt: T, saveLogs: false, evidence: [], version: 1, ...extra,
})

/** Цепочка Pod → PVC → StorageClass (Missing) и ветка к Node (Unknown/forbidden); кандидат — h2. */
export const chainIncident: Incident = incident(report({
  facts: [fact('f1', 'pod.pending_unbound_pvc', podRef), fact('f2', 'pvc.storageclass_missing', pvcRef, { storageClass: 'fast' })],
  hypotheses: [
    { kind: 'hypothesis', id: 'h1', code: 'pending.unbound_pvc', resource: podRef, confidence: 'medium', supporting: ['f1'],
      contradicting: [], ruleId: 'pending.unbound_pvc', ruleEpoch: 1 },
    { kind: 'hypothesis', id: 'h2', code: 'storage.class_missing', resource: pvcRef, params: { storageClass: 'fast' },
      confidence: 'medium', supporting: ['f2'], contradicting: [], ruleId: 'storage.class_missing', ruleEpoch: 1, explains: ['h1'] },
  ],
  recommendations: [{ kind: 'recommendation', id: 'r1', code: 'rec.create_storageclass', resource: pvcRef, hypotheses: ['h2'],
    ruleId: 'storage.class_missing', ruleEpoch: 1 }],
  missing: [{ code: 'graph.unavailable', resource: nodeRef, params: { reason: 'forbidden' } }],
  graph: {
    nodes: [
      { group: 'core', kind: 'Pod', namespace: 'shop', name: 'api-1', health: 'Degraded', reason: 'pending' },
      { group: 'core', kind: 'PersistentVolumeClaim', namespace: 'shop', name: 'data', health: 'Degraded', reason: 'pending' },
      { group: 'storage.k8s.io', kind: 'StorageClass', name: 'fast', health: 'Missing', placeholder: true },
      { group: 'core', kind: 'Node', name: 'n1', health: 'Unknown', unknown: 'forbidden' },
    ],
    edges: [
      { from: 'core/Pod/shop/api-1', to: 'core/PersistentVolumeClaim/shop/data', type: 'mounts', state: 'ok' },
      { from: 'core/PersistentVolumeClaim/shop/data', to: 'storage.k8s.io/StorageClass//fast', type: 'backed_by', state: 'missing' },
    ],
  },
  chains: [
    { path: [{ ref: podRef, health: 'Degraded', reason: 'pending' }, { ref: pvcRef, health: 'Degraded', reason: 'pending' },
      { ref: scRef, health: 'Missing' }], candidate: 'h2' },
    { path: [{ ref: podRef, health: 'Degraded' }, { ref: nodeRef, health: 'Unknown', reason: 'forbidden' }] },
  ] satisfies Chain[],
}))

/** Факты metric.* (значение, единица, окно, агрегат, лимит). */
export const metricsIncident: Incident = incident(report({
  symptoms: ['pod.crash_loop'],
  facts: [
    fact('f1', 'container.restarts', podRef, { container: 'app', count: '7' }),
    fact('f2', 'metric.memory_usage_vs_limit', podRef,
      { container: 'app', value: '0.97', unit: 'ratio', window: '1h0m0s', agg: 'max', limit: '268435456' }, 'memory_usage_vs_limit'),
    fact('f3', 'metric.node_memory_available_ratio', nodeRef,
      { node: 'n1', value: '0.04', unit: 'ratio', window: '30m0s', agg: 'min' }, 'node_memory_available_ratio'),
  ],
  hypotheses: [{ kind: 'hypothesis', id: 'h1', code: 'crash.oom_killed', resource: podRef, params: { container: 'app' },
    confidence: 'medium', supporting: ['f1', 'f2'], contradicting: [], ruleId: 'crash.oom_killed', ruleEpoch: 1 }],
}))

/** Причины отсутствия метрик (signals.Reason): ключ — причина. Первые три — metrics.unavailable, остальные — по сигналу. */
export const metricsGapReasons = ['not_installed', 'forbidden', 'unavailable', 'family_missing', 'no_series', 'history_short', 'invalid_ref'] as const
export const noMetricsIncidents: Record<string, Incident> = Object.fromEntries(metricsGapReasons.map((reason, i) => {
  const missing: MissingContext = i < 3
    ? { code: 'metrics.unavailable', resource: podRef, params: { reason } }
    : { code: 'metrics.signal_unavailable', resource: podRef, params: { signal: 'memory_usage_vs_limit', reason } }
  return [reason, incident(report({ symptoms: ['pod.crash_loop'], missing: [missing] }), { id: `inc-m-${reason}` })]
}))

const workloads: WorkloadRef[] = [0, 1, 2].map((i) => ({
  kind: 'Deployment', namespace: 'shop', name: `web-${i}`, problemPods: 2, symptoms: ['pod.evicted'], since: T,
}))

export const correlationResult: CorrelationResult = {
  version: 1, observedAt: T,
  candidates: [{ id: 'c1', kind: 'shared_node', source: nodeRef, confidence: 'medium', since: T, affected: workloads, total: 12,
    truncated: true, potential: [{ kind: 'Deployment', namespace: 'shop', name: 'cache' }], supporting: ['cf1'] }],
  facts: [fact('cf1', 'correlation.shared_node.memory_pressure', nodeRef)],
  missing: [{ code: 'correlation.unavailable', resource: nodeRef, params: { reason: 'forbidden' } }],
}

/** Инцидент со снимком корреляции, проблемой и сигнатурой. */
export const correlationIncident: Incident = incident(report(), {
  correlation: correlationResult, problemId: 'prob-1', signature: '0123456789abcdef0123456789abcdef',
})

const summary = (id: string): IncidentSummary => ({
  id, target: podRef, status: 'open', openedAt: T, updatedAt: T, openedBy: 'alice', symptoms: ['pod.evicted'], facts: 3,
  hypotheses: 1, problemId: 'prob-1',
})

/** Проблема с двумя участниками, усечённым списком затронутых и отсоединённым расследованием. */
export const problemDetail: ProblemDetail = {
  id: 'prob-1', key: 'shared_node:core/Node//n1', kind: 'shared_node', source: nodeRef, status: 'open', origin: 'auto',
  memberCount: 2, affectedTotal: 12, updatedAt: T,
  members: [{ incidentId: 'inc-1', addedAt: T, addedBy: 'auto', summary: summary('inc-1') },
    { incidentId: 'inc-2', addedAt: T, addedBy: 'alice', summary: summary('inc-2') }],
  affected: workloads, affectedTruncated: true, detached: ['inc-9'],
}

/** Статистика: «доверие есть» (sample ≥ 5) и «доверия нет» (маленькая выборка, без процента). */
export const ruleStatsFixture: RuleStats = {
  records: 12, unevaluated: 3, truncated: false, computedAt: T, ruleSetVersions: [2],
  hypotheses: [
    { ruleId: 'crash.oom_killed', epoch: 1, code: 'crash.oom_killed', confirmed: 6, rejected: 2, sample: 8, trusted: true,
      resolved: 6, recurred: 0, precision: 0.75, medianResolveSeconds: 1800 },
    { ruleId: 'pending.unbound_pvc', epoch: 1, code: 'pending.unbound_pvc', confirmed: 1, rejected: 1, sample: 2, trusted: false,
      resolved: 1, recurred: 0 },
  ],
  recommendations: [
    { ruleId: 'storage.class_missing', epoch: 1, code: 'rec.create_storageclass', helpful: 4, useless: 1, ratio: 0.8, sample: 5, trusted: true },
    { ruleId: 'crash.oom_killed', epoch: 1, code: 'rec.raise_memory_limit', helpful: 0, useless: 4, ratio: 0, sample: 4, trusted: false,
      previouslyUnhelpful: true },
  ],
  symptoms: [{ code: 'pod.crash_loop', closed: 9, withConfirmed: 6, coverage: 0.67 }],
  correlations: [{ kind: 'shared_node', confidence: 'medium', confirmed: 2, rejected: 0, precision: 1 }],
  features: [],
}

/** Инцидент старой версии: ни одного нового поля. */
export const legacyIncident: Incident = incident({
  ruleSetVersion: 1, target: podRef, observedAt: T, symptoms: ['pod.crash_loop'], resources: [podRef],
  facts: [fact('f1', 'container.restarts', podRef, { container: 'app', count: '7' })],
  hypotheses: [{ kind: 'hypothesis', id: 'h1', code: 'crash.application_error', resource: podRef,
    params: { container: 'app', exitCode: '1' }, confidence: 'medium', supporting: ['f1'], contradicting: [] }],
  recommendations: [], steps: [], missing: [],
}, { id: 'inc-old' })
