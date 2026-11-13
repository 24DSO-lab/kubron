<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="qu">
    <div class="rd-row" v-if="scopes.length">
      <span class="rd-label">{{ $t('sections.quotaUsage.scopes') }}</span>
      <span class="rd-value qu-chips">
        <span v-for="s in scopes" :key="s" class="qu-chip">{{ s }}</span>
      </span>
    </div>

    <div class="qu-title">{{ $t('sections.quotaUsage.quotas') }}</div>
    <div v-if="!quotas.length" class="rd-muted">{{ $t('sections.shared.noLimits') }}</div>
    <div v-else class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr><th>{{ $t('sections.shared.resource') }}</th><th>{{ $t('sections.quotaUsage.used') }}</th><th>{{ $t('sections.quotaUsage.limit') }}</th><th class="qu-bar-col">{{ $t('sections.quotaUsage.usage') }}</th></tr>
        </thead>
        <tbody>
          <tr v-for="q in quotas" :key="q.name">
            <td class="rd-mono">{{ q.name }}</td>
            <td>{{ q.used }}</td>
            <td>{{ q.hard }}</td>
            <td>
              <div v-if="q.percent >= 0" class="qu-bar" :title="`${q.percent}%`">
                <div class="qu-fill" :class="level(q.percent)" :style="{ width: Math.min(q.percent, 100) + '%' }" />
                <span class="qu-pct">{{ q.percent }}%</span>
              </div>
              <span v-else class="rd-muted">—</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ resource: any }>()

const quotas = computed<{ name: string; hard: string; used: string; percent: number }[]>(() =>
  Array.isArray(props.resource?.quotas) ? props.resource.quotas : [],
)
const scopes = computed<string[]>(() => (Array.isArray(props.resource?.scopes) ? props.resource.scopes : []))

/** Цвет полосы: ≥100% — ошибка, ≥80% — предупреждение */
const level = (p: number) => (p >= 100 ? 'err' : p >= 80 ? 'warn' : 'ok')
</script>

<style scoped>
.qu-title { font-weight: 700; color: #fff; padding: 12px 0 8px; text-align: left; }
.qu-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.qu-chip {
  padding: 3px 8px; background: var(--panel); border-radius: 3px;
  font-family: 'Courier New', monospace; font-size: 0.8rem; color: var(--text-code);
}
.qu-bar-col { width: 40%; }
.qu-bar {
  position: relative; height: 18px; min-width: 80px; border-radius: 3px;
  background: rgba(255, 255, 255, 0.06); overflow: hidden;
}
.qu-fill { position: absolute; inset: 0 auto 0 0; transition: width 0.3s ease; }
.qu-fill.ok { background: #356546; }
.qu-fill.warn { background: #a8860f; }
.qu-fill.err { background: #b23b3b; }
.qu-pct {
  position: relative; display: block; text-align: center; line-height: 18px;
  font-size: 0.75rem; color: #fff; font-variant-numeric: tabular-nums;
}
</style>
