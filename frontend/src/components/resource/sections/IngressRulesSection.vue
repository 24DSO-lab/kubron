<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ing">
    <div class="ing-title">{{ $t('defs.columns.rules') }}</div>
    <div v-if="rows.length" class="rd-table-wrap">
      <table class="rd-table">
        <thead><tr><th>{{ $t('sections.shared.host') }}</th><th>{{ $t('sections.shared.path') }}</th><th>{{ $t('defs.columns.type') }}</th><th>{{ $t('sections.ingressRules.backend') }}</th></tr></thead>
        <tbody>
          <tr v-for="(r, i) in rows" :key="i">
            <td>{{ r.host }}</td>
            <td class="rd-mono">{{ r.path }}</td>
            <td class="rd-muted">{{ r.pathType || '—' }}</td>
            <td>
              <a v-if="r.backend.service" class="rd-link" @click="openService(r.backend.service)">
                {{ r.backend.service }}:{{ r.backend.port }}
              </a>
              <span v-else class="rd-mono">{{ r.backend.resource || '—' }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="rd-muted">{{ $t('sections.ingressRules.empty') }}</div>

    <template v-if="tls.length">
      <div class="ing-title">{{ $t('defs.columns.tls') }}</div>
      <div class="rd-table-wrap">
        <table class="rd-table">
          <thead><tr><th>{{ $t('defs.columns.hosts') }}</th><th>{{ $t('sections.shared.secret') }}</th></tr></thead>
          <tbody>
            <tr v-for="(t, i) in tls" :key="i">
              <td>{{ (t.hosts ?? []).join(', ') || '—' }}</td>
              <td>
                <a v-if="t.secretName" class="rd-link"
                   @click="emit('open', { id: 'secret', ns: resource.namespace, name: t.secretName })">{{ t.secretName }}</a>
                <template v-else>—</template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { t } from '../../../i18n'

const props = defineProps<{ resource: any }>()
const emit = defineEmits<{ (e: 'open', p: { id: string; ns: string; name: string }): void }>()

const tls = computed<any[]>(() => props.resource?.tlsEntries ?? [])
const rows = computed<any[]>(() => {
  const list = [...(props.resource?.rules ?? [])]
  const d = props.resource?.defaultBackend
  if (d) list.unshift({ host: t('sections.ingressRules.defaultBackend'), path: '—', pathType: '', backend: d })
  return list
})
const openService = (name: string) => emit('open', { id: 'service', ns: props.resource.namespace, name })
</script>

<style scoped>
.ing-title { font-weight: 700; color: var(--text-strong); padding: 12px 0 8px; text-align: left; }
</style>