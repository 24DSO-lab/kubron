<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="nr">
    <div class="nr-title">{{ title }}</div>
    <div v-if="fields.length" class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr><th v-for="f in fields" :key="f.name">{{ f.name }}</th></tr>
        </thead>
        <tbody>
          <tr><td v-for="f in fields" :key="f.name" class="rd-mono">{{ f.value }}</td></tr>
        </tbody>
      </table>
    </div>
    <div v-else class="rd-empty">{{ $t('common.noData') }}</div>
  </div>
</template>

<script lang="ts" setup>
defineProps<{ title: string; fields: { name: string; value: string }[] }>()
</script>

<style scoped>
.nr { padding: 8px 0; text-align: left; }
.nr-title { font-weight: 700; font-size: 1rem; color: #fff; padding: 4px 0 10px; }
.nr .rd-table th { text-transform: none; letter-spacing: 0; font-size: 0.85rem; color: var(--text-code); }
.nr .rd-table td { color: var(--text-soft); }
</style>
