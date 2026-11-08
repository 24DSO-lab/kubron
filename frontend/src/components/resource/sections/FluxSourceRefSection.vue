<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="flux-sourceref" data-testid="flux-sourceref">
    <div class="rd-row">
      <span class="rd-label">{{ $t('flux.sourceRef.source') }}</span>
      <span v-if="source" class="rd-mono" data-testid="flux-sourceref-source">
        <button
          v-if="sourceTarget"
          type="button"
          class="flux-linkbtn"
          @click="openSource"
        >{{ refLabel(source) }}</button>
        <template v-else>{{ refLabel(source) }}</template>
      </span>
      <span v-else class="rd-muted" data-testid="flux-sourceref-none">{{ $t('flux.sourceRef.noSource') }}</span>
    </div>

    <div class="rd-row">
      <span class="rd-label">{{ $t('flux.sourceRef.dependsOn') }}</span>
      <ul v-if="deps.length" class="flux-deps" data-testid="flux-sourceref-deps">
        <li v-for="d in deps" :key="refLabel(d.ref)" class="rd-mono">
          <button
            v-if="d.target"
            type="button"
            class="flux-linkbtn"
            @click="openRef(d.ref)"
          >{{ refLabel(d.ref) }}</button>
          <template v-else>{{ refLabel(d.ref) }}</template>
        </li>
      </ul>
      <span v-else class="rd-muted" data-testid="flux-sourceref-nodeps">{{ $t('flux.sourceRef.noDependsOn') }}</span>
    </div>
  </div>
</template>

<script lang="ts">
import type { FluxRef } from '../../../types/flux'

export interface FluxOpenTarget { id: string; ns: string; name: string }

/** Идентификатор определения ресурса Flux в реестре: flux-<kind в нижнем регистре> (ADR-0023, раздел 9) */
export const fluxResourceId = (kind: string): string => `flux-${kind.toLowerCase()}`

/** Подпись ссылки: Kind namespace/name; у dependsOn вид не указывается — тогда namespace/name */
export function refLabel(ref: FluxRef): string {
  const path = ref.namespace ? `${ref.namespace}/${ref.name}` : ref.name
  return ref.kind ? `${ref.kind} ${path}` : path
}

/**
 * Цель перехода. У dependsOn вид и namespace не заданы: зависимость того же вида и, если namespace пуст,
 * из того же namespace, что и сам объект. `isKnown` — есть ли у вида определение в приложении.
 */
export function refTarget(
  ref: FluxRef,
  self: { kind?: string; namespace?: string },
  isKnown: (id: string) => boolean,
): FluxOpenTarget | null {
  const kind = ref.kind || self.kind || ''
  const name = ref.name
  const ns = ref.namespace || self.namespace || ''
  if (!kind || !name || !ns) return null
  const id = fluxResourceId(kind)
  return isKnown(id) ? { id, ns, name } : null
}
</script>

<script lang="ts" setup>
import { computed } from 'vue'

import type { FluxObjectInfo, FluxRef } from '../../../types/flux'

// sourceRef и dependsOn объекта: переход к объекту, если такой вид открывается в приложении
const props = defineProps<{ resource: Partial<FluxObjectInfo> }>()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string }): void
}>()


const source = computed(() => (props.resource?.sourceRef?.name ? props.resource.sourceRef : null))
const sourceTarget = computed(() => (source.value ? refTarget(source.value, props.resource, () => true) : null))
const deps = computed(() =>
  (props.resource?.dependsOn ?? [])
    .filter(d => !!d?.name)
    .map(d => ({ ref: d, target: refTarget(d, props.resource, () => true) })),
)

async function openSource() {
  if (source.value) await openRef(source.value)
}

async function openRef(ref: FluxRef) {
  const target = refTarget(ref, props.resource, () => true)
  if (!target) return
  // Resolve lazily: importing the registry here avoids a cycle while resource definitions load.
  const { getResourceById } = await import('../../../resources/registry')
  if (getResourceById(target.id)) emit('open', target)
}
</script>

<style scoped>
.flux-deps { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 4px; }
.flux-linkbtn {
  background: none; border: 0; padding: 0; font: inherit; color: #60a5fa; cursor: pointer; text-decoration: underline;
}
.flux-linkbtn:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; border-radius: 2px; }
</style>
