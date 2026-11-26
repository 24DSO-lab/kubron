<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="rd-view">
    <header class="rd-head">
      <div>
        <h2>{{ $t('readiness.title') }}</h2>
        <p class="rd-sub">{{ $t('readiness.subtitle') }}</p>
      </div>
    </header>

    <form class="rd-controls" @submit.prevent="run">
      <label class="field">
        <span>{{ $t('readiness.cluster') }}</span>
        <select v-model="clusterModel" class="form-input" data-testid="readiness-cluster">
          <option value="" disabled>{{ $t('readiness.selectCluster') }}</option>
          <option v-for="c in clusters" :key="c.id" :value="c.id">{{ c.displayName }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ $t('readiness.target') }}</span>
        <select v-model="target" class="form-input" data-testid="readiness-target">
          <option value="">{{ $t('readiness.noTarget') }}</option>
          <option v-for="v in window" :key="v" :value="v">{{ v }}</option>
        </select>
      </label>
      <button class="btn" type="submit" data-testid="readiness-run" :disabled="!clusterId || running">
        {{ running ? $t('readiness.running') : $t('readiness.run') }}
      </button>
    </form>
    <p class="rd-hint">{{ $t('readiness.hint') }}</p>

    <p v-if="error" class="form-error" role="alert">{{ error }}</p>

    <div class="rd-layout">
      <main>
        <p v-if="!report && !running" class="rd-empty">{{ $t('readiness.empty') }}</p>

        <template v-if="report">
          <section class="rd-summary" data-testid="readiness-summary">
            <div class="sum-line">
              <strong>{{ report.current.minor }}</strong> → <strong>{{ report.target || $t('readiness.noTarget') }}</strong>
              <span class="muted">{{ formatDateTime(report.createdAt) }}<template v-if="report.initiator"> · {{ report.initiator }}</template></span>
            </div>
            <div class="sum-counts">
              <span class="c blocker">{{ groups.blocker.length }} {{ $t('readiness.severity.blocker') }}</span>
              <span class="c warning">{{ groups.warning.length }} {{ $t('readiness.severity.warning') }}</span>
              <span class="c info">{{ groups.info.length }} {{ $t('readiness.severity.info') }}</span>
              <span class="c unchecked">{{ unchecked }} {{ $t('readiness.notCheckedShort') }}</span>
            </div>
            <p class="disclaimer">{{ $t('readiness.disclaimer') }}</p>
            <div class="sum-export">
              <button class="btn btn-secondary" type="button" data-testid="readiness-export-json" @click="doExport('json')">{{ $t('readiness.export.json') }}</button>
              <button class="btn btn-secondary" type="button" data-testid="readiness-export-md" @click="doExport('markdown')">{{ $t('readiness.export.markdown') }}</button>
              <span v-if="exportNote" class="muted" role="status">{{ exportNote }}</span>
            </div>
          </section>

          <!-- Coverage показывается всегда и до находок: сначала что проверено и что нет -->
          <ReadinessCoverage :coverage="report.coverage" />

          <section v-if="notChecked.length" class="rd-notchecked" data-testid="readiness-notchecked">
            <h3>{{ $t('readiness.notChecked.title') }}</h3>
            <ul>
              <li v-for="f in notChecked" :key="f.key">{{ messageText(f.explanation) }}</li>
            </ul>
          </section>

          <ReadinessFindings v-for="s in SEVERITIES" :key="s" :severity="s" :findings="groups[s]" />
          <ReadinessDiff :diff="diff" :unavailable="diffUnavailable" />
          <ReadinessActions :actions="report.actions" />
        </template>
      </main>

      <aside>
        <ReadinessHistory :items="history" :selected-id="report?.id ?? ''" :has-more="hasMoreHistory" :loading="loadingHistory"
          @open="open" @more="loadHistory" />
      </aside>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'
import { t } from '../../i18n'
import { formatDateTime } from '../../i18n/format'
import { useClusters } from '../../composables/useClusters'
import { useReadiness } from '../../composables/useReadiness'
import ReadinessActions from '../readiness/ReadinessActions.vue'
import ReadinessCoverage from '../readiness/ReadinessCoverage.vue'
import ReadinessDiff from '../readiness/ReadinessDiff.vue'
import ReadinessFindings from '../readiness/ReadinessFindings.vue'
import ReadinessHistory from '../readiness/ReadinessHistory.vue'
import { SEVERITIES, groupFindings, messageText, notCheckedFindings, partialSources, uncheckedSources } from '../readiness/readinessView'
import type { ExportFormat } from '../../types/readiness'

const { clusters, activeClusterId } = useClusters()
const {
  window, clusterId, target, report, history, hasMoreHistory, diff, diffUnavailable, running, loadingHistory, error,
  loadInfo, selectCluster, loadHistory, run, open, exportReport,
} = useReadiness()

// Выбор кластера: по умолчанию активный; смена сбрасывает отчёт и историю (в composable)
const clusterModel = computed({
  get: () => clusterId.value,
  set: (id: string) => { void selectCluster(id) },
})

const groups = computed(() => groupFindings(report.value?.findings ?? []))
const notChecked = computed(() => notCheckedFindings(report.value?.findings ?? []))
// «Не проверено» = находки «не проверено» + источники, которые отчёт не проверял вовсе или проверил частично
const unchecked = computed(() => {
  const c = report.value?.coverage
  return notChecked.value.length + (c ? uncheckedSources(c).length + partialSources(c).length : 0)
})

const exportNote = ref('')

/** Веб: файл скачивается; десктоп: диалога сохранения нет, поэтому текст копируется в буфер обмена */
async function doExport(format: ExportFormat) {
  exportNote.value = ''
  const out = await exportReport(format)
  if (!out) return
  if (import.meta.env.VITE_TARGET === 'web') {
    exportNote.value = t('readiness.export.saved', { name: out.filename })
    return
  }
  try {
    await navigator.clipboard.writeText(out.content)
    exportNote.value = t('readiness.export.copied')
  } catch {
    exportNote.value = t('readiness.export.copyFailed')
  }
}

onMounted(async () => {
  await loadInfo()
  if (!clusterId.value && activeClusterId.value) await selectCluster(activeClusterId.value)
})
// Переключение активного кластера в шапке переносится и сюда, пока пользователь не выбрал другой вручную
watch(activeClusterId, id => { if (id && id !== clusterId.value) void selectCluster(id) })
</script>

<style scoped>
.rd-view { padding: 24px; text-align: left; max-width: 1400px; }
.rd-head h2 { margin: 0; font-size: 22px; color: #89b4fa; }
.rd-sub { margin: 4px 0 16px; color: #a6adc8; font-size: 13px; }
.rd-controls { display: flex; gap: 12px; align-items: flex-end; flex-wrap: wrap; }
.field { display: grid; gap: 4px; font-size: 12px; color: #a6adc8; }
.field .form-input { min-width: 200px; padding: 8px 10px; }
.rd-hint { margin: 8px 0 16px; font-size: 12px; color: #6c7086; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.rd-layout { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 24px; align-items: start; }
@media (max-width: 1000px) { .rd-layout { grid-template-columns: 1fr; } }
.rd-empty { color: #6c7086; font-size: 13px; }
.rd-summary { border: 1px solid #313244; border-radius: 10px; padding: 14px 16px; margin: 0 0 20px; }
.sum-line { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; font-size: 18px; }
.sum-counts { display: flex; gap: 10px; flex-wrap: wrap; margin: 10px 0; }
.c { padding: 3px 10px; border-radius: 999px; font-size: 12px; font-weight: 600; background: #313244; }
.c.blocker { color: #f38ba8; } .c.warning { color: #f9e2af; } .c.info { color: #89b4fa; } .c.unchecked { color: #fab387; }
.disclaimer { margin: 0 0 10px; font-size: 12px; color: #a6adc8; }
.sum-export { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.muted { color: #6c7086; font-size: 12px; }
.rd-notchecked { border: 1px solid rgba(250, 179, 135, 0.5); background: rgba(250, 179, 135, 0.06); border-radius: 10px; padding: 12px 16px; margin: 0 0 20px; }
.rd-notchecked h3 { margin: 0 0 6px; font-size: 15px; color: #fab387; }
.rd-notchecked ul { margin: 0; padding-left: 18px; font-size: 13px; line-height: 1.5; }
</style>
