// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { FluxRef } from '../../../types/flux'

export interface FluxOpenTarget { id: string; ns: string; name: string }
export const fluxResourceId = (kind: string): string => `flux-${kind.toLowerCase()}`

// Keep this lightweight allowlist independent from registry.ts: resource definitions import
// this section, so importing the registry here would create a module cycle.
const FLUX_RESOURCE_IDS: ReadonlySet<string> = new Set([
  'flux-kustomization', 'flux-helmrelease', 'flux-gitrepository', 'flux-ocirepository',
  'flux-helmrepository', 'flux-helmchart', 'flux-bucket', 'flux-alert', 'flux-provider',
  'flux-receiver', 'flux-imagerepository', 'flux-imagepolicy', 'flux-imageupdateautomation',
])

export const isKnownFluxResource = (id: string): boolean => FLUX_RESOURCE_IDS.has(id.toLowerCase())
export function refLabel(ref: FluxRef): string {
  const path = ref.namespace ? `${ref.namespace}/${ref.name}` : ref.name
  return ref.kind ? `${ref.kind} ${path}` : path
}
export function refTarget(ref: FluxRef, self: { kind?: string; namespace?: string }, isKnown: (id: string) => boolean): FluxOpenTarget | null {
  const kind = ref.kind || self.kind || ''
  const name = ref.name
  const ns = ref.namespace || self.namespace || ''
  if (!kind || !name || !ns) return null
  const id = fluxResourceId(kind)
  return isKnown(id) ? { id, ns, name } : null
}
