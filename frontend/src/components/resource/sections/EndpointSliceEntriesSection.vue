<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="rd-wide">
    <div class="es-title">{{ $t('defs.columns.endpoints') }}</div>
    <div v-if="entries.length" class="rd-table-wrap">
      <table class="rd-table">
        <thead><tr><th>{{ $t('sections.shared.address') }}</th><th>{{ $t('defs.columns.node') }}</th><th>{{ $t('sections.shared.zone') }}</th><th>{{ $t('defs.columns.target') }}</th><th>{{ $t('defs.columns.ready') }}</th><th>{{ $t('sections.endpointSliceEntries.serving') }}</th><th>{{ $t('sections.endpointSliceEntries.terminating') }}</th></tr></thead>
        <tbody>
          <tr v-for="(e, i) in entries" :key="i">
            <td class="rd-mono">{{ e.address }}</td>
            <td>
              <a v-if="e.node" class="rd-link" @click="emit('open', { id: 'node', ns: '', name: e.node })">{{ e.node }}</a>
              <template v-else>—</template>
            </td>
            <td>{{ e.zone || '—' }}</td>
            <td>
              <a v-if="e.target" class="rd-link"
                 @click="emit('open', { id: String(e.target.kind).toLowerCase(), ns: e.target.namespace || resource.namespace, name: e.target.name })">
                {{ e.target.kind }}/{{ e.target.name }}
              </a>
              <template v-else>—</template>
            </td>
            <td><span class="rd-badge" :class="e.ready ? 'rd-ok' : 'rd-warn'">{{ e.ready }}</span></td>
            <td><span class="rd-badge" :class="e.serving ? 'rd-ok' : 'rd-warn'">{{ e.serving }}</span></td>
            <td><span class="rd-badge" :class="e.terminating ? 'rd-warn' : 'rd-unk'">{{ e.terminating }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="rd-muted">{{ $t('sections.endpointSliceEntries.empty') }}</div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()
const emit = defineEmits<{ (e: 'open', p: { id: string; ns: string; name: string }): void }>()
const entries = computed<any[]>(() => props.resource?.entries ?? [])
</script>

<style scoped>
.es-title { font-weight: 700; color: var(--text-strong); padding: 8px 0; text-align: left; }
</style>