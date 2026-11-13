<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="np">
    <template v-for="d in dirs" :key="d.key">
      <div class="np-title">{{ d.title }}</div>
      <div v-if="!d.active" class="rd-muted">{{ $t('sections.networkPolicyRules.notApplied') }}</div>
      <div v-else-if="!d.rules.length" class="rd-muted">{{ d.denied }}</div>
      <div v-else class="rd-table-wrap">
        <table class="rd-table">
          <thead><tr><th>{{ d.peerLabel }}</th><th>{{ $t('defs.columns.ports') }}</th></tr></thead>
          <tbody>
            <tr v-for="(r, i) in d.rules" :key="i">
              <td>
                <div v-for="p in r.peers" :key="p" class="rd-mono">{{ p }}</div>
                <span v-if="!r.peers?.length" class="rd-muted">{{ $t('sections.shared.all') }}</span>
              </td>
              <td>
                <div v-for="p in r.ports" :key="p" class="rd-mono">{{ p }}</div>
                <span v-if="!r.ports?.length" class="rd-muted">{{ $t('sections.shared.all') }}</span>
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
const types = computed<string[]>(() => props.resource?.policyTypes ?? [])

const dirs = computed(() => [
  { key: 'ingress', title: t('defs.props.ingressRules'), peerLabel: t('sections.networkPolicyRules.from'), denied: t('sections.networkPolicyRules.deniedIngress'), active: types.value.includes('Ingress'), rules: props.resource?.ingress ?? [] },
  { key: 'egress', title: t('defs.props.egressRules'), peerLabel: t('sections.networkPolicyRules.to'), denied: t('sections.networkPolicyRules.deniedEgress'), active: types.value.includes('Egress'), rules: props.resource?.egress ?? [] },
])
</script>

<style scoped>
.np-title { font-weight: 700; color: var(--text-strong); padding: 12px 0 8px; text-align: left; }
</style>