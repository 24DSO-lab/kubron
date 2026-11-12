// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref } from 'vue'
import { errorText } from './errorText'
import { downloadTextFile } from '../platform/files'
import {
  readinessCompare, readinessExport, readinessGet, readinessHistory, readinessInfo, readinessRun,
} from '../platform/readiness'
import type { Diff, ExportFormat, ExportedReport, ReadinessInfo, Report, Summary } from '../types/readiness'

const PAGE_SIZE = 20

/**
 * Состояние экрана Upgrade Readiness: окно целевых версий, текущий отчёт, история кластера и сравнение с прошлой
 * проверкой. Одно состояние на приложение; при смене кластера всё сбрасывается. Ответы, пришедшие после смены
 * кластера или нового запроса, отбрасываются.
 */
const info = ref<ReadinessInfo | null>(null)
const clusterId = ref('')
const target = ref('') // '' — цель не выбрана
const report = ref<Report | null>(null)
const history = ref<Summary[]>([])
const nextCursor = ref('')
const diff = ref<Diff | null>(null)
const diffUnavailable = ref(false) // у отчёта нет более раннего: сравнивать не с чем
const running = ref(false)
const loadingHistory = ref(false)
const error = ref('')

// «Поколение» состояния: растёт при смене кластера и при каждом запросе отчёта.
let clusterGen = 0
let reportSeq = 0

function reset() {
  clusterGen++
  reportSeq++
  report.value = null
  history.value = []
  nextCursor.value = ''
  diff.value = null
  diffUnavailable.value = false
  error.value = ''
  running.value = false
  loadingHistory.value = false
}

async function loadInfo() {
  if (info.value) return
  try {
    info.value = await readinessInfo()
    // Цель по умолчанию не выбирается: пользователь решает сам (без цели проверяются только уже устаревшие API).
  } catch (e) {
    error.value = errorText(e)
  }
}

/** Выбор кластера; пустой id — выход. Сбрасывает отчёт и подгружает историю. */
async function selectCluster(id: string) {
  if (id === clusterId.value) return
  clusterId.value = id
  reset()
  if (id) await loadHistory(true)
}

async function loadHistory(first = false) {
  const id = clusterId.value
  if (!id || loadingHistory.value) return
  const gen = clusterGen
  loadingHistory.value = true
  try {
    const page = await readinessHistory(id, first ? '' : nextCursor.value, PAGE_SIZE)
    if (gen !== clusterGen) return
    history.value = first ? (page.items ?? []) : [...history.value, ...(page.items ?? [])]
    nextCursor.value = page.next ?? ''
  } catch (e) {
    if (gen === clusterGen) error.value = errorText(e)
  } finally {
    if (gen === clusterGen) loadingHistory.value = false
  }
}

/** Запускает проверку выбранного кластера и целевой версии */
async function run() {
  const id = clusterId.value
  if (!id || running.value) return
  const gen = clusterGen
  const seq = ++reportSeq
  running.value = true
  error.value = ''
  try {
    const rep = await readinessRun(id, target.value)
    if (gen !== clusterGen || seq !== reportSeq) return
    report.value = rep
    await Promise.all([loadHistory(true), loadDiff(rep.id)])
  } catch (e) {
    if (gen === clusterGen && seq === reportSeq) error.value = errorText(e)
  } finally {
    if (gen === clusterGen) running.value = false
  }
}

/** Открывает сохранённый отчёт из истории */
async function open(reportId: string) {
  const id = clusterId.value
  if (!id) return
  const gen = clusterGen
  const seq = ++reportSeq
  error.value = ''
  try {
    const rep = await readinessGet(id, reportId)
    if (gen !== clusterGen || seq !== reportSeq) return
    report.value = rep
    await loadDiff(rep.id)
  } catch (e) {
    if (gen === clusterGen && seq === reportSeq) error.value = errorText(e)
  }
}

/** «Что изменилось с прошлой проверки»: сравнение отчёта с ближайшим более ранним */
async function loadDiff(reportId: string) {
  const id = clusterId.value
  const gen = clusterGen
  const seq = reportSeq
  diff.value = null
  diffUnavailable.value = false
  try {
    const d = await readinessCompare(id, reportId)
    if (gen !== clusterGen || seq !== reportSeq) return
    diff.value = d
  } catch {
    // Первый отчёт кластера: предыдущего нет. Это штатный случай, а не ошибка экрана.
    if (gen === clusterGen && seq === reportSeq) diffUnavailable.value = true
  }
}

/** Выгрузка отчёта: файл в вебе, текст для буфера обмена в десктопе (там нет диалога сохранения) */
async function exportReport(format: ExportFormat): Promise<ExportedReport | null> {
  const id = clusterId.value
  const rep = report.value
  if (!id || !rep) return null
  try {
    const out = await readinessExport(id, rep.id, format)
    if (import.meta.env.VITE_TARGET === 'web') downloadTextFile(out.filename, out.content, out.mime)
    return out
  } catch (e) {
    error.value = errorText(e)
    return null
  }
}

const window = computed(() => info.value?.window ?? [])

export function useReadiness() {
  return {
    info: computed(() => info.value), window, clusterId: readonly(clusterId), target, report: computed(() => report.value), history: computed(() => history.value),
    hasMoreHistory: computed(() => nextCursor.value !== ''), diff: computed(() => diff.value), diffUnavailable: readonly(diffUnavailable),
    running: readonly(running), loadingHistory: readonly(loadingHistory), error: readonly(error),
    loadInfo, selectCluster, loadHistory: () => loadHistory(false), run, open, exportReport,
  }
}

/** Только для тестов: возвращает модуль в исходное состояние */
export function resetReadinessForTests() {
  info.value = null
  clusterId.value = ''
  target.value = ''
  reset()
}
