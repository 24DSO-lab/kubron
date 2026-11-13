<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="rd-row rd-click" @click="open = !open">
    <span class="rd-label">{{ $t('defs.columns.ports') }}<span class="rd-arrow" :class="{ open }">›</span></span>
    <span v-if="!open" class="rd-muted">{{ $t('sections.servicePorts.count', ports.length) }}</span>
    <div v-else-if="ports.length" class="rd-wide" @click.stop>
      <div v-for="p in ports" :key="p.port" class="svc-port">
        <span class="svc-port-label">{{ p.port }}:{{ p.targetPort }}/{{ p.protocol || 'TCP' }}</span>
        <span v-if="p.name" class="svc-port-name">({{ p.name }})</span>
        <span v-if="p.nodePort" class="svc-port-nodeport">NodePort: {{ p.nodePort }}</span>
        <PortForwardControl
          :namespace="resource.namespace"
          :resource-name="resource.name"
          :port="p.port"
          resource-type="service"
        />
      </div>
    </div>
    <span v-else class="rd-muted">—</span>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed } from 'vue'
import PortForwardControl from './PortForwardControl.vue'

const props = defineProps<{ resource: any }>()
const open = ref(false)
const ports = computed(() => props.resource?.ports ?? [])
</script>

<style scoped>
.svc-port { display: flex; align-items: center; gap: 8px; padding: 6px 0; border-bottom: 1px solid rgba(255,255,255,0.05); }
.svc-port:last-child { border-bottom: none; }
.svc-port-label { color: var(--blue); font-weight: 500; font-size: 0.875rem; }
.svc-port-name { color: var(--text-gray); font-size: 0.8rem; }
.svc-port-nodeport { color: #f59e0b; font-size: 0.8rem; font-weight: 500; }
</style>
