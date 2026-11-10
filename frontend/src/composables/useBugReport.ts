// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref } from 'vue'
import { i18n } from '../i18n'
import { captureScreenshot } from './captureScreenshot'
import { errorText } from './errorText'
import { pngBase64 } from '../components/bugreport/bugReportView'
import { exportBugReports, getBugReportStatus, submitBugReport } from '../platform/bugreport'
import type { BugReportResult, BugReportStatus } from '../types/bugreport'

type Phase = 'closed' | 'capturing' | 'open' | 'sending'

const CONTACT_KEY = 'kubron.bugreport.contact'

function loadContact(): string {
  try { return localStorage.getItem(CONTACT_KEY) ?? '' } catch { return '' }
}
function saveContact(v: string) {
  try { v ? localStorage.setItem(CONTACT_KEY, v) : localStorage.removeItem(CONTACT_KEY) } catch { /* необязательное удобство */ }
}

// Одно состояние на приложение: черновик переживает «Сделать снимок заново» и закрытие диалога по ошибке отправки.
const phase = ref<Phase>('closed')
const screenshot = ref('') // PNG data URL; пусто — снимка нет
const captureFailed = ref(false)
const includeScreenshot = ref(true)
const description = ref('')
const contact = ref(loadContact())
const error = ref('')
const status = ref<BugReportStatus | null>(null)
const view = ref('')
const exportedPath = ref('') // архив ожидающих отчётов, сохранённый из диалога

async function refreshStatus() {
  try {
    status.value = await getBugReportStatus()
  } catch (e) {
    status.value = null
    error.value = errorText(e)
  }
}

/** Снимок берётся до показа диалога, чтобы на нём был экран с ошибкой, а не само окно отчёта. */
async function capture() {
  captureFailed.value = false
  try {
    screenshot.value = await captureScreenshot()
  } catch {
    screenshot.value = ''
    captureFailed.value = true
  }
  includeScreenshot.value = !!screenshot.value
}

/** Начать отчёт: снимок экрана, затем диалог. currentView — раздел интерфейса, он попадёт в отчёт. */
async function start(currentView: string) {
  if (phase.value !== 'closed') return
  view.value = currentView
  error.value = ''
  exportedPath.value = ''
  phase.value = 'capturing'
  // Дадим интерфейсу перерисоваться (закрыть меню, убрать фокус), потом снимаем.
  await new Promise<void>((r) => requestAnimationFrame(() => r()))
  await Promise.all([capture(), refreshStatus()])
  phase.value = 'open'
}

/** Новый снимок: диалог на время скрывается, иначе он попал бы в кадр. */
async function retake() {
  if (phase.value !== 'open') return
  phase.value = 'capturing'
  await new Promise<void>((r) => requestAnimationFrame(() => r()))
  await capture()
  phase.value = 'open'
}

function buildRequest() {
  return {
    description: description.value.trim(),
    contact: contact.value.trim(),
    screenshot: includeScreenshot.value ? pngBase64(screenshot.value) : '',
    locale: String(i18n.global.locale.value),
    view: view.value,
  }
}

/**
 * Отправить отчёт. Результат говорит, что с ним стало: «sent» — ушёл, «queued» — сохранён на этом компьютере и уйдёт
 * сам, когда появится связь. При ошибке диалог остаётся открытым с введённым текстом. null — отправить не удалось.
 */
async function send(): Promise<BugReportResult | null> {
  if (phase.value !== 'open') return null
  error.value = ''
  phase.value = 'sending'
  try {
    saveContact(contact.value.trim())
    const result = await submitBugReport(buildRequest())
    close()
    return result
  } catch (e) {
    error.value = errorText(e)
    phase.value = 'open'
    return null
  }
}

/** Сохранить ожидающие отчёты архивом (для компьютера без связи); путь показывается в диалоге. */
async function exportPending(): Promise<boolean> {
  if (phase.value !== 'open') return false
  error.value = ''
  try {
    exportedPath.value = await exportBugReports()
    return true
  } catch (e) {
    error.value = errorText(e)
    return false
  }
}

/** Закрыть и забыть черновик и снимок (снимок может содержать чувствительные данные). */
function close() {
  phase.value = 'closed'
  screenshot.value = ''
  description.value = ''
  error.value = ''
  exportedPath.value = ''
  captureFailed.value = false
}

export function useBugReport() {
  return {
    phase: readonly(phase),
    visible: computed(() => phase.value === 'open' || phase.value === 'sending'),
    screenshot: readonly(screenshot),
    captureFailed: readonly(captureFailed),
    includeScreenshot,
    description,
    contact,
    error: readonly(error),
    status: readonly(status),
    exportedPath: readonly(exportedPath),
    start, retake, send, exportPending, close, refreshStatus,
  }
}
