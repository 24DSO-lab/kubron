// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * «Сообщить об ошибке» (десктоп): RPC к internal/app/app_bugreport.go. Единственное место, где фронтенд зовёт
 * GetBugReportStatus / SaveBugReportSettings / ClearBugReportSettings / SubmitBugReport / ExportBugReports. Результаты приводятся к
 * типам из types/bugreport.ts, потому что Wails описывает структуры Go классами.
 */
import { callApp } from './api'
import type { BugReportRequest, BugReportResult, BugReportSettings, BugReportStatus } from '../types/bugreport'

export async function getBugReportStatus(): Promise<BugReportStatus> {
  return (await callApp('GetBugReportStatus')) as unknown as BugReportStatus
}

export async function saveBugReportSettings(settings: Omit<BugReportSettings, 'passwordSet'>): Promise<void> {
  await callApp('SaveBugReportSettings', { ...settings, passwordSet: false } as never)
}

export async function clearBugReportSettings(): Promise<void> {
  await callApp('ClearBugReportSettings')
}

/** Отправляет отчёт; в ответе: ушёл он или сохранён до появления связи */
export async function submitBugReport(req: BugReportRequest): Promise<BugReportResult> {
  return (await callApp('SubmitBugReport', req as never)) as unknown as BugReportResult
}

/** Сохраняет ожидающие отчёты одним архивом, показывает его в файловом менеджере и возвращает путь (десктоп) */
export async function exportBugReports(): Promise<string> {
  return (await callApp('ExportBugReports')) as unknown as string
}
