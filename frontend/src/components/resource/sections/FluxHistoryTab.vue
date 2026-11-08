<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="rows.length" class="rd-table-wrap" data-testid="flux-history">
    <table class="rd-table">
      <thead>
        <tr>
          <th>{{ $t('fluxHelm.history.release') }}</th>
          <th>{{ $t('fluxHelm.history.chart') }}</th>
          <th>{{ $t('fluxHelm.history.appVersion') }}</th>
          <th>{{ $t('common.status') }}</th>
          <th>{{ $t('fluxHelm.history.deployed') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="h in rows" :key="h.version" data-testid="flux-history-row">
          <td class="rd-mono">#{{ h.version }}</td>
          <td class="rd-mono">{{ chartLabel(h.chartName, h.chartVersion) || $t('common.dash') }}</td>
          <td class="rd-mono">{{ h.appVersion || $t('common.dash') }}</td>
          <td><span class="rd-badge" :class="badgeClass(h.status)">{{ h.status || $t('common.unknown') }}</span></td>
          <td class="rd-muted">{{ timeOf(h.lastDeployed) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
  <div v-else class="rd-empty" data-testid="flux-history-empty">{{ $t('fluxHelm.history.empty') }}</div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '../../../i18n/format'
import { chartLabel } from '../../../resources/flux'
import { badgeClass } from '../../../resources/status'
import type { FluxHelmReleaseInfo } from '../../../types/flux'
import { sortHistory } from './fluxHistory'

// История Helm-релиза (status.history): версия релиза, чарт, appVersion, статус, время
const props = defineProps<{ resource: Partial<FluxHelmReleaseInfo> }>()
const { t } = useI18n()
const rows = computed(() => sortHistory(props.resource?.history))

function timeOf(v?: string): string {
  return v && !Number.isNaN(Date.parse(v)) ? formatDateTime(v) : t('common.dash')
}
</script>
