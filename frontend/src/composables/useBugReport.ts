// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref } from 'vue'
import { i18n } from '../i18n'
import { captureScreenshot } from './captureScreenshot'
import { errorText } from './errorText'
import { pngBase64 } from '../components/bugreport/bugReportView'
import { copyImageToClipboard } from './clipboardImage'
import { getBugReportStatus, openBugReportDraft, revealBugReportFile, submitBugReport } from '../platform/bugreport'
import type { BugReportDraft, BugReportStatus } from '../types/bugreport'

type Phase = 'closed' | 'capturing' | 'open' | 'sending' | 'sent' | 'draft'

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
// Результат «Открыть в почтовой программе»: что подготовлено и попал ли снимок в буфер обмена
const draft = ref<(BugReportDraft & { copied: boolean }) | null>(null)

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

/** Начать отчёт: снимок экрана, затем диалог. currentView — раздел интерфейса, он попадёт в письмо. */
async function start(currentView: string) {
  if (phase.value !== 'closed') return
  view.value = currentView
  error.value = ''
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
 * Открыть почтовую программу с готовым письмом (десктоп, без почтового сервера). Ссылка mailto не умеет вложений,
 * поэтому снимок кладётся в буфер обмена (вставить Ctrl+V) и сохраняется файлом. Запись в буфер начинается сразу,
 * до первого await: браузер требует действия пользователя.
 */
async function openInMailClient(): Promise<boolean> {
  if (phase.value !== 'open') return false
  const req = buildRequest()
  const copying = req.screenshot ? copyImageToClipboard(screenshot.value) : Promise.resolve(false)
  error.value = ''
  phase.value = 'sending'
  try {
    saveContact(contact.value.trim())
    const files = await openBugReportDraft(req)
    draft.value = { ...files, copied: await copying }
    phase.value = 'draft'
    return true
  } catch (e) {
    error.value = errorText(e)
    phase.value = 'open'
    return false
  }
}

/** Показать сохранённый файл в файловом менеджере; ошибка показывается в диалоге. */
async function revealFile(path: string) {
  try {
    await revealBugReportFile(path)
  } catch (e) {
    error.value = errorText(e)
  }
}

/** Вернуться к отчёту из подсказки (например, если почтовая программа не открылась). */
function backToReport() {
  if (phase.value === 'draft') {
    draft.value = null
    error.value = ''
    phase.value = 'open'
  }
}

async function send(): Promise<boolean> {
  if (phase.value !== 'open') return false
  error.value = ''
  phase.value = 'sending'
  try {
    saveContact(contact.value.trim())
    await submitBugReport(buildRequest())
    phase.value = 'sent'
    close()
    return true
  } catch (e) {
    error.value = errorText(e)
    phase.value = 'open'
    return false
  }
}

/** Закрыть и забыть черновик и снимок (снимок может содержать чувствительные данные). */
function close() {
  phase.value = 'closed'
  screenshot.value = ''
  description.value = ''
  error.value = ''
  captureFailed.value = false
  draft.value = null
}

export function useBugReport() {
  return {
    phase: readonly(phase),
    visible: computed(() => phase.value === 'open' || phase.value === 'sending' || phase.value === 'draft'),
    draft: readonly(draft),
    screenshot: readonly(screenshot),
    captureFailed: readonly(captureFailed),
    includeScreenshot,
    description,
    contact,
    error: readonly(error),
    status: readonly(status),
    start, retake, send, openInMailClient, revealFile, backToReport, close, refreshStatus,
  }
}
