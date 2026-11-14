<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="conn-tab">
    <div class="rd-row">
      <span class="rd-label">{{ $t('defs.columns.clusterIp') }}</span>
      <span class="rd-value">{{ resource.clusterIP || '—' }}</span>
    </div>
    <div v-if="resource.clusterIPs?.length" class="rd-row">
      <span class="rd-label">{{ $t('sections.connection.clusterIps') }}</span>
      <span class="rd-value">{{ resource.clusterIPs.join(', ') }}</span>
    </div>
    <div class="rd-row">
      <span class="rd-label">{{ $t('sections.connection.ipFamilies') }}</span>
      <span class="rd-value">{{ resource.ipFamilies || '—' }}</span>
    </div>
    <div class="rd-row">
      <span class="rd-label">{{ $t('sections.connection.ipFamilyPolicy') }}</span>
      <span class="rd-value">{{ resource.ipFamilyPolicy || '—' }}</span>
    </div>
    <div class="rd-row">
      <span class="rd-label">{{ $t('sections.connection.externalIps') }}</span>
      <span class="rd-value">{{ resource.externalIPs?.join(', ') || '—' }}</span>
    </div>
    <div v-if="ports.length" class="rd-row">
      <span class="rd-label">{{ $t('defs.columns.ports') }}</span>
      <div class="rd-value conn-ports">
        <div v-for="p in ports" :key="p.port + '/' + p.protocol" class="conn-port">
          <span class="conn-port-main">{{ formatPort(p) }}</span>
          <div v-if="resource.externalName" class="rd-row">
            <span class="rd-label">{{ $t('defs.props.externalName') }}</span>
            <span class="rd-value">{{ resource.externalName }}</span>
          </div>
          <div v-if="resource.loadBalancer?.length" class="rd-row">
            <span class="rd-label">{{ $t('defs.props.loadBalancer') }}</span>
            <span class="rd-value">{{ resource.loadBalancer.join(', ') }}</span>
          </div>
          <PortForwardControl :namespace="resource.namespace" :resource-name="resource.name" :port="p.port"
            resource-type="service" />
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import PortForwardControl from './PortForwardControl.vue'

const props = defineProps<{ resource: any }>()

const ports = computed(() => props.resource?.ports ?? [])

function formatPort(p: any): string {
  let s = p.name ? `${p.name}: ` : ''
  s += `${p.port}`
  if (p.targetPort && String(p.targetPort) !== String(p.port)) s += `→${p.targetPort}`
  if (p.nodePort) s += ` (NodePort ${p.nodePort})`
  return `${s}/${p.protocol || 'TCP'}`
}
</script>

<style scoped>
.conn-tab {
  padding: 12px 16px;
}

.conn-ports {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.conn-port {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.conn-port-main {
  color: var(--blue);
  font-weight: 500;
  font-size: 0.875rem;
}
</style>
