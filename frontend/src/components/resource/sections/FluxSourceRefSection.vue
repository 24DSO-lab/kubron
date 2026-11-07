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
          @click="emit('open', sourceTarget)"
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
            @click="emit('open', d.target)"
          >{{ refLabel(d.ref) }}</button>
          <template v-else>{{ refLabel(d.ref) }}</template>
        </li>
      </ul>
      <span v-else class="rd-muted" data-testid="flux-sourceref-nodeps">{{ $t('flux.sourceRef.noDependsOn') }}</span>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { getResourceById } from '../../../resources/registry'
import { refLabel, refTarget } from './fluxSourceRef'
import type { FluxObjectInfo } from '../../../types/flux'

// sourceRef и dependsOn объекта: переход к объекту, если такой вид открывается в приложении
const props = defineProps<{ resource: Partial<FluxObjectInfo> }>()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string }): void
}>()

const isKnown = (id: string) => !!getResourceById(id)

const source = computed(() => (props.resource?.sourceRef?.name ? props.resource.sourceRef : null))
const sourceTarget = computed(() => (source.value ? refTarget(source.value, props.resource, isKnown) : null))
const deps = computed(() =>
  (props.resource?.dependsOn ?? [])
    .filter(d => !!d?.name)
    .map(d => ({ ref: d, target: refTarget(d, props.resource, isKnown) })),
)
</script>

<style scoped>
.flux-deps { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 4px; }
.flux-linkbtn {
  background: none; border: 0; padding: 0; font: inherit; color: #60a5fa; cursor: pointer; text-decoration: underline;
}
.flux-linkbtn:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; border-radius: 2px; }
</style>
