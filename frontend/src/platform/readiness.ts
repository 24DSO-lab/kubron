// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Upgrade Readiness: RPC к internal/app/app_readiness.go. Единственное место, где фронтенд зовёт Readiness*.
 * Ответы приводятся к типам из types/readiness.ts: Wails описывает структуры Go классами.
 */
import { callApp } from './api'
import type { Diff, ExportFormat, ExportedReport, HistoryPage, ReadinessInfo, Report } from '../types/readiness'

export async function readinessInfo(): Promise<ReadinessInfo> {
  return (await callApp('ReadinessInfo')) as unknown as ReadinessInfo
}

/** Запускает проверку и сохраняет отчёт; target — «1.36» или '' (цель не выбрана) */
export async function readinessRun(clusterId: string, target: string): Promise<Report> {
  return (await callApp('ReadinessRun', clusterId, target)) as unknown as Report
}

export async function readinessHistory(clusterId: string, cursor: string, limit: number): Promise<HistoryPage> {
  return (await callApp('ReadinessHistory', clusterId, cursor, limit)) as unknown as HistoryPage
}

export async function readinessGet(clusterId: string, id: string): Promise<Report> {
  return (await callApp('ReadinessGet', clusterId, id)) as unknown as Report
}

/** Сравнение с предыдущим отчётом кластера (previousId пуст — с ближайшим более ранним) */
export async function readinessCompare(clusterId: string, id: string, previousId = ''): Promise<Diff> {
  return (await callApp('ReadinessCompare', clusterId, id, previousId)) as unknown as Diff
}

export async function readinessExport(clusterId: string, id: string, format: ExportFormat): Promise<ExportedReport> {
  return (await callApp('ReadinessExport', clusterId, id, format)) as unknown as ExportedReport
}
