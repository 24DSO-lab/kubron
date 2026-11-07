// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { FluxRef } from '../../../types/flux'

export interface FluxOpenTarget { id: string; ns: string; name: string }
export const fluxResourceId = (kind: string): string => `flux-${kind.toLowerCase()}`
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
