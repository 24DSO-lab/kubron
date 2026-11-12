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
  OutcomeResult,
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
 */
export function useIncidents(clusterId: () => string) {
  const items = ref<IncidentSummary[]>([])
  const nextCursor = ref('')
  const status = ref<IncidentStatus | ''>('')
  const loading = ref(false)
  const detail = ref<IncidentDetail | null>(null)
  const detailLoading = ref(false)
  const settings = ref<IncidentSettingsInfo | null>(null)

  /** Право читать содержимое фрагментов: только подсказка интерфейсу, сервер проверяет его при каждом чтении */
  const canReadLogs = computed(() => can(Perm.DiagnosticsLogs))

  async function loadList(more = false): Promise<void> {
    loading.value = true
    try {
      const page = await callApp('IncidentList', clusterId(), {
        limit: PAGE_SIZE, cursor: more ? nextCursor.value : '', status: status.value,
      } as never)
      const got = (page as { items?: IncidentSummary[]; nextCursor?: string }) ?? {}
      items.value = more ? [...items.value, ...(got.items ?? [])] : (got.items ?? [])
      nextCursor.value = got.nextCursor ?? ''
    } finally {
      loading.value = false
    }
  }

  async function open(req: OpenIncidentRequest): Promise<IncidentDetail> {
    const d = normalizeDetail((await callApp('IncidentOpen', clusterId(), req as never)) as IncidentDetail)
    detail.value = d
    return d
  }

  async function select(id: string): Promise<void> {
    detailLoading.value = true
    try {
      detail.value = normalizeDetail((await callApp('IncidentGet', clusterId(), id)) as IncidentDetail)
    } finally {
      detailLoading.value = false
    }
  }

  function close(): void {
    detail.value = null
  }

  async function refreshEvents(): Promise<void> {
    if (!detail.value) return
    detail.value = normalizeDetail((await callApp('IncidentRefresh', clusterId(), detail.value.incident.id)) as IncidentDetail)
  }

  async function markOutcome(result: OutcomeResult, confirmedHypothesisId: string, note: string): Promise<void> {
    if (!detail.value) return
    detail.value = normalizeDetail((await callApp('IncidentMarkOutcome', clusterId(), detail.value.incident.id, {
      result, confirmedHypothesisId, note,
    } as never)) as IncidentDetail)
  }

  async function saveFragment(pod: string, container: string, previous: boolean): Promise<void> {
    if (!detail.value) return
    const id = detail.value.incident.id
    await callApp('IncidentSaveFragment', clusterId(), id, { pod, container, previous, anchorLine: 0 } as never)
    await select(id)
  }

  async function deleteFragment(fragmentId: string): Promise<void> {
    if (!detail.value) return
    const id = detail.value.incident.id
    await callApp('IncidentDeleteFragment', clusterId(), id, fragmentId)
    await select(id)
  }

  async function setLogCapture(enabled: boolean, deleteExisting: boolean): Promise<void> {
    if (!detail.value) return
    detail.value = normalizeDetail((await callApp('IncidentSetLogCapture', clusterId(), detail.value.incident.id,
      enabled, deleteExisting)) as IncidentDetail)
  }

  /** Содержимое фрагмента: сервер проверяет право diagnostics.logs при каждом вызове */
  async function readFragment(fragmentId: string): Promise<Fragment> {
    if (!detail.value) throw new Error('no incident selected')
    return (await callApp('IncidentReadFragment', clusterId(), detail.value.incident.id, fragmentId)) as Fragment
  }

  async function remove(): Promise<void> {
    if (!detail.value) return
    await callApp('IncidentDelete', clusterId(), detail.value.incident.id)
    detail.value = null
    await loadList()
  }

  async function exportIncident(includeFragments: boolean): Promise<void> {
    if (!detail.value) return
    const id = detail.value.incident.id
    const text = (await callApp('IncidentExport', clusterId(), id, includeFragments)) as string
    downloadTextFile(`kubron-incident-${id}.json`, text, 'application/json')
  }

  async function loadSettings(): Promise<void> {
    settings.value = (await callApp('IncidentGetSettings', clusterId())) as IncidentSettingsInfo
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
    loadList, open, select, close, refreshEvents, markOutcome, saveFragment, deleteFragment, setLogCapture,
    readFragment, remove, exportIncident, loadSettings, saveSettings, listen,
  }
}
