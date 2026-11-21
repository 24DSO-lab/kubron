// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import { callApp } from '../platform/api'
import { downloadTextFile } from '../platform/files'
import { can } from './useAuth'
import { normalizeIncident } from './incidentModel'
import { Perm } from '../types/auth'
import type { Incident, IncidentDetail, RuleStats, RuleStatsExport } from '../types/incident'

/** Выгруженный файл приходит в base64 (ExportFileDTO); текст в UTF-8 восстанавливается без потерь. */
export function decodeBase64Utf8(b64: string): string {
  const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

/** Go отдаёт nil-срезы как null; интерфейс работает с массивами. */
function normalizeStats(s: RuleStats): RuleStats {
  return {
    ...s,
    ruleSetVersions: s.ruleSetVersions ?? [],
    hypotheses: s.hypotheses ?? [],
    recommendations: s.recommendations ?? [],
    symptoms: s.symptoms ?? [],
    correlations: s.correlations ?? [],
    features: s.features ?? [],
  }
}

/**
 * Что нужно расследованию сверх его самого (ADR-0034): статистика правил кластера и предыдущее расследование
 * при повторении. Ответы после смены кластера или устаревшие отбрасываются. Ошибка статистики не мешает
 * показать расследование: пометки просто не выводятся.
 */
export function useIncidentInsights(clusterId: () => string) {
  const stats = ref<RuleStats | null>(null)
  const statsLoading = ref(false)
  const previous = ref<Incident | null>(null)
  /** id, для которого загружено или загружается предыдущее расследование */
  const previousFor = ref('')

  let gen = 0
  let statsSeq = 0
  let prevSeq = 0

  /** Право выгрузки — то же, что у настроек хранения (users.manage); подсказка интерфейсу, сервер проверит заново. */
  const canExport = computed(() => can(Perm.UsersManage))

  function reset(): void {
    gen++
    statsSeq++
    prevSeq++
    stats.value = null
    statsLoading.value = false
    previous.value = null
    previousFor.value = ''
  }

  async function loadStats(): Promise<void> {
    const g = gen
    const seq = ++statsSeq
    statsLoading.value = true
    try {
      const s = (await callApp('IncidentRuleStats', clusterId())) as RuleStats | null
      if (g !== gen || seq !== statsSeq) return
      stats.value = s ? normalizeStats(s) : null
    } catch {
      // Нет статистики — нет пометок; расследование читается и так.
      if (g === gen && seq === statsSeq) stats.value = null
    } finally {
      if (g === gen && seq === statsSeq) statsLoading.value = false
    }
  }

  /** Предыдущее расследование для баннера повторения; недоступное (удалено, нет прав) — `null`, баннер без подробностей. */
  async function loadPrevious(id: string): Promise<void> {
    const g = gen
    const seq = ++prevSeq
    previousFor.value = id
    previous.value = null
    if (!id) return
    try {
      const d = (await callApp('IncidentGet', clusterId(), id)) as IncidentDetail
      if (g !== gen || seq !== prevSeq) return
      previous.value = normalizeIncident(d.incident)
    } catch {
      if (g === gen && seq === prevSeq) previous.value = null
    }
  }

  function clearPrevious(): void {
    prevSeq++
    previous.value = null
    previousFor.value = ''
  }

  /** Выгрузка анонимной статистики файлом: только по явному действию, без автоматической отправки. */
  async function exportStats(): Promise<void> {
    const f = (await callApp('IncidentRuleStatsExport', clusterId())) as RuleStatsExport
    downloadTextFile(f.filename, decodeBase64Utf8(f.dataBase64), f.mime || 'application/json')
  }

  return { stats, statsLoading, previous, previousFor, canExport, reset, loadStats, loadPrevious, clearPrevious, exportStats }
}
