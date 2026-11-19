// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Фикстуры Kubron Intelligence (JSON, формат — docs/prompts/contracts.md §3–7).
 * Интерфейс работает на них до появления реальных RPC (D7); здесь они только получают типы.
 * Золотые файлы D3/D4/D6 при сверке кладутся рядом и подключаются так же.
 */
import type { Incident, ProblemDetail, RuleStats } from '../../../types/incident'
import chain from './report-chain.json'
import metrics from './report-metrics.json'
import noMetrics from './report-no-metrics.json'
import correlation from './incident-correlation.json'
import problem from './problem.json'
import ruleStats from './rule-stats.json'
import legacy from './incident-legacy.json'

/** Цепочка Pod → PVC → StorageClass (Missing) и ветка к Node (Unknown/forbidden); кандидат — h2. */
export const chainIncident = chain as unknown as Incident
/** Факты metric.* (значение, единица, окно, агрегат, лимит). */
export const metricsIncident = metrics as unknown as Incident
/** Метрик нет: metrics.unavailable и metrics.signal_unavailable со всеми причинами. */
export const noMetricsIncident = noMetrics as unknown as Incident
/** Снимок корреляции, проблема и сигнатура. */
export const correlationIncident = correlation as unknown as Incident
/** Проблема с двумя участниками, усечённым списком затронутых и отсоединённым расследованием. */
export const problemDetail = problem as unknown as ProblemDetail
/** Статистика: «доверие есть» (sample ≥ 5) и «доверия нет» (без процента). */
export const ruleStatsFixture = ruleStats as unknown as RuleStats
/** Инцидент старой версии: ни одного нового поля. */
export const legacyIncident = legacy as unknown as Incident
