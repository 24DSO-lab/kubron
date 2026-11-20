// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import { callApp } from '../platform/api'
import { onEvent } from '../platform/events'
import { downloadTextFile } from '../platform/files'
import { can } from './useAuth'
import { Perm } from '../types/auth'
import { normalizeIncident } from './incidentModel'
import type {
  Fragment,
  IncidentDetail,
  IncidentSettings,
  IncidentSettingsInfo,
  IncidentStatus,
  IncidentSummary,
  OpenIncidentRequest,
  OutcomeInput,
} from '../types/incident'

/** Имя события бэкенда: снимок Events расследования обновлён (internal/app.EventIncidentUpdated) */
export const INCIDENT_UPDATED_EVENT = 'incident:updated'

const PAGE_SIZE = 25

function normalizeDetail(d: IncidentDetail): IncidentDetail {
  return { incident: normalizeIncident(d.incident), fragments: d.fragments ?? [] }
}

/**
 * Расследования Kubron Intelligence: история, открытие, детали, результат, фрагменты логов, настройки.
 * Состояние принадлежит экземпляру (на экран); кластер задаёт вызывающий. Ошибки бэкенда пробрасываются
 * вызывающему, который показывает их через errorText.
 *
 * Ответы, пришедшие после смены кластера (`reset`) или после более нового запроса того же рода, отбрасываются:
 * устаревший ответ не перезаписывает свежий, а его ошибка не показывается.
 */
export function useIncidents(clusterId: () => string) {
  const items = ref<IncidentSummary[]>([])
  const nextCursor = ref('')
  const status = ref<IncidentStatus | ''>('')
  const loading = ref(false)
  const detail = ref<IncidentDetail | null>(null)
  const detailLoading = ref(false)
  const settings = ref<IncidentSettingsInfo | null>(null)

  // «Поколение» состояния растёт при смене кластера; счётчики списка и деталей — при каждом новом запросе.
  let gen = 0
  let listSeq = 0
  let detailSeq = 0

  /** Право читать содержимое фрагментов: только подсказка интерфейсу, сервер проверяет его при каждом чтении */
  const canReadLogs = computed(() => can(Perm.DiagnosticsLogs))

  /** Сброс при смене кластера: данные прежнего кластера не должны остаться на экране, а их ответы — дойти до него */
  function reset(): void {
    gen++
    listSeq++
    detailSeq++
    items.value = []
    nextCursor.value = ''
    loading.value = false
    detail.value = null
    detailLoading.value = false
    settings.value = null
  }

  async function loadList(more = false): Promise<void> {
    const g = gen
    const seq = ++listSeq
    loading.value = true
    try {
      const page = await callApp('IncidentList', clusterId(), {
        limit: PAGE_SIZE, cursor: more ? nextCursor.value : '', status: status.value,
      } as never)
      if (g !== gen || seq !== listSeq) return
      const got = (page as { items?: IncidentSummary[]; nextCursor?: string }) ?? {}
      items.value = more ? [...items.value, ...(got.items ?? [])] : (got.items ?? [])
      nextCursor.value = got.nextCursor ?? ''
    } catch (e) {
      if (g !== gen || seq !== listSeq) return
      throw e
    } finally {
      if (g === gen && seq === listSeq) loading.value = false
    }
  }

  async function open(req: OpenIncidentRequest): Promise<IncidentDetail> {
    const g = gen
    const seq = ++detailSeq
    const d = normalizeDetail((await callApp('IncidentOpen', clusterId(), req as never)) as IncidentDetail)
    if (g === gen && seq === detailSeq) detail.value = d
    return d
  }

  async function select(id: string): Promise<void> {
    const g = gen
    const seq = ++detailSeq
    detailLoading.value = true
    try {
      const d = normalizeDetail((await callApp('IncidentGet', clusterId(), id)) as IncidentDetail)
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

  /**
   * Применяет ответ действия над открытым расследованием: только если кластер не менялся и на экране всё ещё то же
   * расследование. Иначе ответ устарел и показывать его нельзя.
   */
  function applyDetail(g: number, id: string, d: IncidentDetail): void {
    if (g === gen && detail.value?.incident.id === id) detail.value = d
  }

  async function refreshEvents(): Promise<void> {
    if (!detail.value) return
    const g = gen
    const id = detail.value.incident.id
    applyDetail(g, id, normalizeDetail((await callApp('IncidentRefresh', clusterId(), id)) as IncidentDetail))
  }

  /** Отметка результата: в запрос уходят только заполненные поля (ADR-0034 §2), «не отмечено» — отсутствие поля. */
  async function markOutcome(input: OutcomeInput): Promise<void> {
    if (!detail.value) return
    const g = gen
    const id = detail.value.incident.id
    applyDetail(g, id, normalizeDetail((await callApp('IncidentMarkOutcome', clusterId(), id, input as never)) as IncidentDetail))
  }

  async function saveFragment(pod: string, container: string, previous: boolean): Promise<void> {
    if (!detail.value) return
    const g = gen
    const id = detail.value.incident.id
    await callApp('IncidentSaveFragment', clusterId(), id, { pod, container, previous, anchorLine: 0 } as never)
    if (g === gen && detail.value?.incident.id === id) await select(id)
  }

  async function deleteFragment(fragmentId: string): Promise<void> {
    if (!detail.value) return
    const g = gen
    const id = detail.value.incident.id
    await callApp('IncidentDeleteFragment', clusterId(), id, fragmentId)
    if (g === gen && detail.value?.incident.id === id) await select(id)
  }

  async function setLogCapture(enabled: boolean, deleteExisting: boolean): Promise<void> {
    if (!detail.value) return
    const g = gen
    const id = detail.value.incident.id
    applyDetail(g, id, normalizeDetail((await callApp('IncidentSetLogCapture', clusterId(), id,
      enabled, deleteExisting)) as IncidentDetail))
  }

  /** Содержимое фрагмента: сервер проверяет право diagnostics.logs при каждом вызове */
  async function readFragment(fragmentId: string): Promise<Fragment> {
    if (!detail.value) throw new Error('no incident selected')
    return (await callApp('IncidentReadFragment', clusterId(), detail.value.incident.id, fragmentId)) as Fragment
  }

  async function remove(): Promise<void> {
    if (!detail.value) return
    const g = gen
    const id = detail.value.incident.id
    await callApp('IncidentDelete', clusterId(), id)
    if (g !== gen) return
    if (detail.value?.incident.id === id) close()
    await loadList()
  }

  async function exportIncident(includeFragments: boolean): Promise<void> {
    if (!detail.value) return
    const id = detail.value.incident.id
    const text = (await callApp('IncidentExport', clusterId(), id, includeFragments)) as string
    downloadTextFile(`kubron-incident-${id}.json`, text, 'application/json')
  }

  async function loadSettings(): Promise<void> {
    const g = gen
    const s = (await callApp('IncidentGetSettings', clusterId())) as IncidentSettingsInfo
    if (g === gen) settings.value = s
  }

  async function saveSettings(s: IncidentSettings): Promise<void> {
    const saved = (await callApp('IncidentSaveSettings', s as never)) as IncidentSettings
    if (settings.value) settings.value = { ...settings.value, settings: saved }
  }

  /** Подписка на обновление Events открытого расследования; возвращает отписку. */
  function listen(): () => void {
    return onEvent(INCIDENT_UPDATED_EVENT, (id: unknown) => {
      if (detail.value && detail.value.incident.id === id && detail.value.incident.status === 'open') void select(String(id))
    })
  }

  return {
    items, nextCursor, status, loading, detail, detailLoading, settings, canReadLogs,
    reset, loadList, open, select, close, refreshEvents, markOutcome, saveFragment, deleteFragment, setLogCapture,
    readFragment, remove, exportIncident, loadSettings, saveSettings, listen,
  }
}
