<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="hist" data-testid="readiness-history">
    <h3>{{ $t('readiness.history.title') }}</h3>
    <p v-if="!items.length && !loading" class="muted">{{ $t('readiness.history.empty') }}</p>
    <ul v-else class="rows">
      <li v-for="s in items" :key="s.id">
        <button type="button" :class="['row', { active: s.id === selectedId }]" @click="emit('open', s.id)">
          <span class="when">{{ formatDateTime(s.createdAt) }}</span>
          <span class="ver">{{ s.currentVersion }} → {{ s.targetVersion || $t('readiness.noTarget') }}</span>
          <span class="counts">
            <span class="b" :title="$t('readiness.severity.blocker')">{{ s.blockers }}</span>
            <span class="w" :title="$t('readiness.severity.warning')">{{ s.warnings }}</span>
            <span class="i" :title="$t('readiness.severity.info')">{{ s.infos }}</span>
            <span v-if="s.notChecked" class="n" :title="$t('readiness.history.notChecked')">⚠ {{ s.notChecked }}</span>
          </span>
          <span v-if="s.initiator" class="muted">{{ s.initiator }}</span>
        </button>
      </li>
    </ul>
    <button v-if="hasMore" class="btn btn-secondary" type="button" :disabled="loading" @click="emit('more')">{{ $t('readiness.history.more') }}</button>
  </section>
</template>

<script lang="ts" setup>
import { formatDateTime } from '../../i18n/format'
import type { Summary } from '../../types/readiness'

defineProps<{ items: readonly Summary[]; selectedId: string; hasMore: boolean; loading: boolean }>()
const emit = defineEmits<{ open: [id: string]; more: [] }>()
</script>

<style scoped>
.hist h3 { margin: 0 0 8px; font-size: 15px; }
.rows { list-style: none; margin: 0 0 8px; padding: 0; display: grid; gap: 4px; }
.row { width: 100%; display: grid; grid-template-columns: 1fr auto; gap: 2px 10px; text-align: left; background: transparent; color: inherit;
  border: 1px solid #313244; border-radius: 8px; padding: 8px 10px; cursor: pointer; font-size: 12px; }
.row:hover { background: #232334; } .row.active { border-color: #89b4fa; background: rgba(137, 180, 250, 0.08); }
.when { font-weight: 600; } .ver { color: #a6adc8; }
.counts { grid-column: 2; grid-row: 1 / span 2; display: flex; gap: 6px; align-items: center; }
.counts span { padding: 1px 6px; border-radius: 4px; background: #313244; }
.b { color: #f38ba8; } .w { color: #f9e2af; } .i { color: #89b4fa; } .n { color: #fab387; }
.muted { color: #6c7086; font-size: 12px; }
</style>
