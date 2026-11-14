<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="rd-row rd-click" @click="open = !open">
    <span class="rd-label">{{ $t('sections.podVolumes.title') }}<span class="rd-arrow" :class="{ open }">›</span></span>
    <span v-if="!open" class="rd-muted">{{ $t('sections.podVolumes.groups', groups.length) }}</span>
    <div v-else-if="groups.length" class="rd-wide" @click.stop>
      <div v-for="g in groups" :key="g.type" class="vol-group">
        <div class="vol-type">{{ g.type }}</div>
        <table class="rd-table">
          <thead>
            <tr>
              <th v-for="col in g.columns" :key="col">{{ col === 'Name' ? $t('common.name') : col }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in g.rows" :key="i">
              <td v-for="col in g.columns" :key="col">{{ row[col] ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <span v-else class="rd-muted">—</span>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed } from 'vue'

const props = defineProps<{ resource: any }>()
const open = ref(false)

const groups = computed(() => {
  const vols: any[] = props.resource?.volumes ?? []
  const byType = new Map<string, any[]>()
  for (const v of vols) {
    const list = byType.get(v.type) ?? []
    list.push(v)
    byType.set(v.type, list)
  }
  const out: { type: string; columns: string[]; rows: Record<string, string>[] }[] = []
  for (const [type, list] of byType) {
    const colSet = new Set<string>(['Name'])
    const rows: Record<string, string>[] = []
    for (const v of list) {
      const row: Record<string, string> = { Name: v.name }
      for (const [k, val] of Object.entries(v.details ?? {})) {
        colSet.add(k)
        row[k] = String(val)
      }
      rows.push(row)
    }
    out.push({ type, columns: Array.from(colSet), rows })
  }
  return out
})
</script>

<style scoped>
.vol-group { margin-bottom: 12px; }
.vol-type { font-weight: 600; font-size: 0.85rem; color: var(--text-gray); margin-bottom: 6px; text-transform: capitalize; }
</style>
