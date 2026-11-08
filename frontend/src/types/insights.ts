// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Зеркало Go: internal/k8s/overview_risks.go, overview_changes.go, overview_certs.go */
export interface NodeHeadroom {
  name: string
  cpuFree: number
  cpuAllocatable: number
  memoryFree: number
  memoryAllocatable: number
}
export interface QuotaPressure { namespace: string; name: string; resource: string; used: string; hard: string; percent: number }
export interface PDBBlock { namespace: string; name: string; healthy: number; desired: number; expected: number }
export interface OverviewRisks {
  nodes: NodeHeadroom[]
  quotas: QuotaPressure[]
  quotaTotal: number
  pdbs: PDBBlock[]
  pdbTotal: number
}

export interface ChangeEvent {
  kind: 'Rollout' | 'Helm' | 'Argo CD' | 'Flux' | string
  name: string
  namespace: string
  detail: string
  /** RFC3339 */
  time: string
  /** id ресурса для открытия панели; пусто — открывать нечего */
  target: string
}

export interface CertExpiry {
  namespace: string
  name: string
  subject: string
  notAfter: string
  /** отрицательное — уже истёк */
  daysLeft: number
  managed: boolean
}
export interface CertScan { checked: number; expiring: CertExpiry[] }
