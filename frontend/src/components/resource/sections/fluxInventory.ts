// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { FluxGraph, FluxGraphNode } from '../../../types/flux'

export const WORKLOAD_KINDS: ReadonlySet<string> = new Set(['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod'])
export type InventoryMode = 'none' | 'unavailable' | 'remote' | 'list'

export function inventoryMode(g: FluxGraph | null | undefined): InventoryMode {
  if (!g) return 'none'
  if (!g.available) return 'unavailable'
  if (g.remote) return 'remote'
  return 'list'
}
export function resourceNodes(g: FluxGraph | null | undefined): FluxGraphNode[] {
  return (g?.nodes ?? []).filter(n => n?.role === 'resource' && !!n.name)
}
const byIdentity = (a: FluxGraphNode, b: FluxGraphNode) => a.kind.localeCompare(b.kind) || a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name)
export function splitInventory(nodes: readonly FluxGraphNode[], query = '', kind = ''): { workloads: FluxGraphNode[]; others: FluxGraphNode[] } {
  const q = query.trim().toLowerCase()
  const kept = nodes.filter(n => !kind || n.kind === kind).filter(n => !q || `${n.kind} ${n.name} ${n.namespace}`.toLowerCase().includes(q)).sort(byIdentity)
  return { workloads: kept.filter(n => WORKLOAD_KINDS.has(n.kind)), others: kept.filter(n => !WORKLOAD_KINDS.has(n.kind)) }
}
