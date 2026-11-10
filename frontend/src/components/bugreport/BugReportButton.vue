<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="capabilities.bugReport" class="bug-report">
    <button type="button" class="bug-btn" :disabled="report.phase.value !== 'closed'" data-testid="bug-report-button"
      :title="$t('bugReport.button')" :aria-label="$t('bugReport.button')" @click="report.start(view)">
      <span aria-hidden="true">🐞</span>
      <span class="bug-label">{{ $t('bugReport.button') }}</span>
    </button>
    <BugReportDialog v-if="report.visible.value" />
  </div>
</template>

<script lang="ts" setup>
import { useBugReport } from '../../composables/useBugReport'
import { useCapabilities } from '../../composables/useCapabilities'
import BugReportDialog from './BugReportDialog.vue'

// Кнопка в шапке. Видна, только если оболочка умеет отправлять отчёты (capabilities.bugReport, десктоп).
defineProps<{ view: string }>()
const { capabilities } = useCapabilities()
const report = useBugReport()
</script>

<style scoped>
.bug-report { margin-right: 10px; }
.bug-btn {
  display: flex; align-items: center; gap: 6px; background: #1e1e2e; border: 1px solid #313244;
  border-radius: 999px; padding: 5px 12px; color: #cdd6f4; cursor: pointer; font-size: 13px;
}
.bug-btn:hover:not(:disabled) { border-color: #45475a; }
.bug-btn:disabled { opacity: 0.6; cursor: progress; }
@media (max-width: 1100px) { .bug-label { display: none; } }
</style>
