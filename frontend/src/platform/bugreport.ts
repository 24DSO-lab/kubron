// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * «Сообщить об ошибке» (десктоп): RPC к internal/app/app_bugreport.go. Единственное место, где фронтенд зовёт
 * GetBugReportStatus / SaveBugReportSettings / ClearBugReportSettings / SubmitBugReport. Результаты приводятся к
 * типам из types/bugreport.ts, потому что Wails описывает структуры Go классами.
 */
import { callApp } from './api'
import type { BugReportDraft, BugReportRequest, BugReportSettings, BugReportStatus } from '../types/bugreport'

export async function getBugReportStatus(): Promise<BugReportStatus> {
  return (await callApp('GetBugReportStatus')) as unknown as BugReportStatus
}

export async function saveBugReportSettings(settings: Omit<BugReportSettings, 'passwordSet'>): Promise<void> {
  await callApp('SaveBugReportSettings', { ...settings, passwordSet: false } as never)
}

export async function clearBugReportSettings(): Promise<void> {
  await callApp('ClearBugReportSettings')
}

export async function submitBugReport(req: BugReportRequest): Promise<void> {
  await callApp('SubmitBugReport', req as never)
}

/** Открывает почтовую программу с готовым письмом; снимок и длинный текст сохраняет файлами (десктоп). */
export async function openBugReportDraft(req: BugReportRequest): Promise<BugReportDraft> {
  return (await callApp('OpenBugReportDraft', req as never)) as unknown as BugReportDraft
}

/** Показывает сохранённый файл в файловом менеджере системы (десктоп). */
export async function revealBugReportFile(path: string): Promise<void> {
  await callApp('RevealBugReportFile', path as never)
}
