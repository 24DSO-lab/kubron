<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="rd-row rd-click" @click="open = !open">
    <span class="rd-label">{{ $t('defs.columns.endpoints') }}<span class="rd-arrow" :class="{ open }">›</span></span>
    <span v-if="!open" class="rd-muted">{{ $t('sections.endpoints.count', endpoints.length) }}</span>
    <div v-else-if="endpoints.length" class="rd-wide" @click.stop>
      <div v-for="ep in endpoints" :key="ep.name" class="ep-item">
        <span class="ep-name">{{ ep.name }}</span>
        <span class="ep-addrs">{{ ep.addresses?.join(', ') || '—' }}</span>
      </div>
    </div>
    <span v-else class="rd-muted">{{ $t('common.none') }}</span>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed } from 'vue'

const props = defineProps<{ resource: any }>()
const open = ref(false)
const endpoints = computed(() => props.resource?.endpoints ?? [])
</script>

<style scoped>
.ep-item { padding: 6px 0; border-bottom: 1px solid rgba(255,255,255,0.05); font-size: 0.875rem; }
.ep-item:last-child { border-bottom: none; }
.ep-name { display: block; font-weight: 600; color: #c0c2d8; margin-bottom: 2px; }
.ep-addrs { display: block; color: var(--text-soft); font-size: 0.8rem; }
</style>
