<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="content-header">
    <div class="content-header-left">
      <h2>
        <K8sIcon v-if="icon" :name="icon" size="lg" class="header-icon" />
        {{ title }}
      </h2>
      <span class="count" v-if="count !== undefined && count >= 0">{{ $t('app.content.count', count) }}</span>
    </div>
    <input v-if="showSearch" :value="search" @input="$emit('search', ($event.target as HTMLInputElement).value)" :placeholder="$t('app.content.searchPlaceholder')" class="search-input"/>
    <span v-if="updatedAt" class="content-updated">{{ $t('app.content.updated', { time: updatedAt }) }}</span>
    <span v-else class="content-updated content-updated--empty">&nbsp;</span>
    <button type="button" :disabled="refreshing" @click="$emit('refresh')" class="ov-btn">
      {{ refreshing ? $t('app.content.refreshing') : $t('common.refresh') }}
    </button>
  </div>
</template>

<script lang="ts" setup>
import K8sIcon from './icons/K8sIcon.vue'

defineProps<{ icon?: string; title: string; count?: number; search?: string; showSearch?: boolean; refreshing?: boolean; updatedAt?: string }>()
defineEmits<{ (e: 'refresh'): void; (e: 'search', val: string): void }>()
</script>

<style scoped>
.content-header { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
.content-header-left { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
.content-header h2 { font-size: 18px; color: var(--accent); margin: 0; }
.header-icon {
  margin-right: 6px;
  vertical-align: middle;
}
.count { font-size: 13px; color: var(--text-muted); white-space: nowrap; }
.search-input {
  padding: 6px 12px; background: #26283a; color: var(--text);
  border: 1px solid var(--line-strong); border-radius: 6px; font-size: 13px; width: 200px;
}
.search-input:focus { outline: none; border-color: var(--accent); }
.content-updated { font-size: 12px; color: #8b91ad; margin-left: auto; }
.ov-btn {
  padding: 7px 14px;
  width: 110px;
  background: #1a1d2e;
  border: 1px solid #8b5cf6;
  border-radius: 8px;
  font: inherit;
  font-size: 13px;
  font-weight: 500;
  color: #e6e8f5;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}
.ov-btn:hover:not(:disabled) { background: #20243a; border-color: #a78bfa; }
.ov-btn:disabled { opacity: 0.6; cursor: default; }
</style>