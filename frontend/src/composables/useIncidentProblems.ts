// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import { callApp } from '../platform/api'
import { onEvent } from '../platform/events'
import { can } from './useAuth'
import { Perm } from '../types/auth'
import type { ProblemDetail, ProblemStatus, ProblemSummary } from '../types/incident'

/** Адресное событие бэкенда (ADR-0009): несёт только ID проблемы, подробности читаются RPC с проверкой прав. */
export const PROBLEM_CHANGED_EVENT = 'problem:changed'

const PAGE_SIZE = 25

/** Go отдаёт nil-срезы как null; интерфейс работает с массивами. */
function normalizeDetail(d: ProblemDetail): ProblemDetail {
  return {
    ...d,
    members: d.members ?? [],
    affected: d.affected ?? [],
    detached: d.detached ?? [],
  }
}

/**
 * Проблемы Kubron Intelligence (ADR-0033 §7): список, экран проблемы и ручные действия над составом.
 * Действия только меняют группировку: расследования не удаляются и не сливаются.
 * Ответы после смены кластера (`reset`) или устаревшие ответы того же рода отбрасываются.
 */
export function useIncidentProblems(clusterId: () => string) {
  const items = ref<ProblemSummary[]>([])
  const nextCursor = ref('')
  const status = ref<ProblemStatus | ''>('open')
  const loading = ref(false)
  const detail = ref<ProblemDetail | null>(null)
  const detailLoading = ref(false)

  let gen = 0
  let listSeq = 0
  let detailSeq = 0

  /** Только подсказка интерфейсу: сервер проверяет право `operate` при каждом действии. */
  const canOperate = computed(() => can(Perm.Operate))

  function reset(): void {
    gen++
    listSeq++
    detailSeq++
    items.value = []
    nextCursor.value = ''
    loading.value = false
    detail.value = null
    detailLoading.value = false
  }

  async function loadList(more = false): Promise<void> {
    const g = gen
    const seq = ++listSeq
    loading.value = true
    try {
      const page = (await callApp('IncidentProblemList', clusterId(), {
        status: status.value, limit: PAGE_SIZE, cursor: more ? nextCursor.value : '',
      } as never)) as { items?: ProblemSummary[]; nextCursor?: string } | null
      if (g !== gen || seq !== listSeq) return
      const got = page ?? {}
      items.value = more ? [...items.value, ...(got.items ?? [])] : (got.items ?? [])
      nextCursor.value = got.nextCursor ?? ''
    } catch (e) {
      if (g !== gen || seq !== listSeq) return
      throw e
    } finally {
      if (g === gen && seq === listSeq) loading.value = false
    }
  }

  async function select(id: string): Promise<void> {
    const g = gen
    const seq = ++detailSeq
    detailLoading.value = true
    try {
      const d = normalizeDetail((await callApp('IncidentProblemGet', clusterId(), id)) as ProblemDetail)
      if (g !== gen || seq !== detailSeq) return
      detail.value = d
    } catch (e) {
      if (g !== gen || seq !== detailSeq) return
      throw e
    } finally {
      if (g === gen && seq === detailSeq) detailLoading.value = false
    }
  }

  function close(): void {
    detailSeq++
    detailLoading.value = false
    detail.value = null
  }

  /** Ответ действия применяется, только если кластер не менялся; список перечитывается, чтобы счётчики не отстали. */
  async function applyResult(g: number, d: ProblemDetail): Promise<void> {
    if (g !== gen) return
    detailSeq++
    detail.value = normalizeDetail(d)
    await loadList().catch(() => undefined)
  }

  async function attach(incidentId: string): Promise<void> {
    if (!detail.value) return
    const g = gen
    await applyResult(g, (await callApp('IncidentProblemAttach', clusterId(), detail.value.id, incidentId)) as ProblemDetail)
  }

  /** Отсоединяет расследование; открытая проблема перечитывается, а если её больше нет — экран закрывается. */
  async function detach(incidentId: string): Promise<void> {
    const g = gen
    const id = detail.value?.id
    await callApp('IncidentProblemDetach', clusterId(), incidentId)
    if (g !== gen) return
    if (id && detail.value?.id === id) {
      try {
        await select(id)
      } catch {
        close()
      }
    }
    await loadList().catch(() => undefined)
  }

  /** Объединяет открытую проблему в `toId`; на экране остаётся результат. */
  async function merge(toId: string): Promise<void> {
    if (!detail.value) return
    const g = gen
    await applyResult(g, (await callApp('IncidentProblemMerge', clusterId(), detail.value.id, toId)) as ProblemDetail)
  }

  async function create(incidentIds: string[]): Promise<void> {
    const g = gen
    await applyResult(g, (await callApp('IncidentProblemCreate', clusterId(), incidentIds)) as ProblemDetail)
  }

  /** Подписка на `problem:changed`: список перечитывается, открытая проблема — если событие о неё. */
  function listen(): () => void {
    return onEvent(PROBLEM_CHANGED_EVENT, (id: unknown) => {
      void loadList().catch(() => undefined)
      if (detail.value && detail.value.id === id) {
        void select(String(id)).catch(() => close())
      }
    })
  }

  return { items, nextCursor, status, loading, detail, detailLoading, canOperate, reset, loadList, select, close, attach, detach, merge, create, listen }
}
