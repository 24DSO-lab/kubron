// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import { callApp } from '../resources/wails'
import { errorText } from './errorText'
import type {
  Baseline, BaselineCheckResult, BaselineException, BaselinePreview, BaselinePriority, BaselineRef,
  BaselineResultRow, BaselineSpec, BaselineStatus, BaselineVersionSummary,
} from '../types/baseline'

const PAGE_SIZE = 20

/** Порядок статусов в таблице результата: сначала то, что требует внимания */
const STATUS_ORDER: Record<BaselineStatus, number> = { changed: 0, missing: 1, unchecked: 2, new: 3, match: 4 }
const PRIORITY_RANK: Record<BaselinePriority, number> = { high: 3, medium: 2, low: 1 }

export function statusOrder(s: BaselineStatus): number { return STATUS_ORDER[s] ?? 9 }
export function priorityRank(p?: BaselinePriority): number { return p ? PRIORITY_RANK[p] ?? 0 : 0 }

/** «Не проверено» никогда не считается «соответствует»: класс оформления отличается от success */
export function statusTone(s: BaselineStatus): 'ok' | 'warn' | 'bad' | 'muted' {
  switch (s) {
    case 'match': return 'ok'
    case 'changed': return 'warn'
    case 'missing': return 'bad'
    default: return 'muted'
  }
}

/** Разбор поля «пространства имён»: запятые и пробелы, без пустых и повторов */
export function parseList(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).map(s => s.trim()).filter(Boolean))]
}

/** Подпись ресурса: kind ns/name */
export function refLabel(r: BaselineRef): string {
  return `${r.kind} ${r.namespace ? `${r.namespace}/` : ''}${r.name}`.trim()
}

export function emptySpec(): BaselineSpec {
  return { namespaces: [], selector: '', kinds: [], refs: [] }
}

/** Спецификация пригодна для предпросмотра: есть пространства имён или явный список ресурсов */
export function specReady(s: BaselineSpec): boolean {
  return s.namespaces.length > 0 || s.refs.length > 0
}

/** Строки результата в порядке показа: статус, затем приоритет (важные выше), затем ресурс */
export function sortItems<T extends { status: BaselineStatus; priority?: BaselinePriority; ref: BaselineRef }>(items: T[]): T[] {
  return [...items].sort((a, b) =>
    statusOrder(a.status) - statusOrder(b.status)
    || priorityRank(b.priority) - priorityRank(a.priority)
    || refLabel(a.ref).localeCompare(refLabel(b.ref)))
}

/** Предпросмотр пуст: нечего фиксировать, создавать версию нельзя */
export function previewEmpty(p: BaselinePreview | null): boolean {
  return !p || p.objects === 0
}

/**
 * Состояние раздела baseline: список, выбранный baseline, история версий и проверок, исключения, последний результат.
 * Все обращения идут через callApp; ошибки бэкенда показываются через errorText.
 */
export function useBaseline() {
  const baselines = ref<Baseline[]>([])
  const selectedId = ref('')
  const versions = ref<BaselineVersionSummary[]>([])
  const versionsNext = ref('')
  const results = ref<BaselineResultRow[]>([])
  const resultsNext = ref('')
  const exceptions = ref<BaselineException[]>([])
  const result = ref<BaselineCheckResult | null>(null)
  const kinds = ref<string[]>([])
  const loading = ref(false)
  const checking = ref(false)
  const error = ref('')

  const selected = computed(() => baselines.value.find(b => b.id === selectedId.value) ?? null)
  const activeVersion = computed(() => versions.value.find(v => v.id === selected.value?.activeVersionId) ?? null)

  async function guard<T>(fn: () => Promise<T>): Promise<T | undefined> {
    error.value = ''
    try { return await fn() } catch (e) { error.value = errorText(e); return undefined }
  }

  async function loadList() {
    loading.value = true
    const list = await guard(() => callApp('BaselineList'))
    loading.value = false
    if (!list) return
    baselines.value = list as Baseline[]
    if (!baselines.value.some(b => b.id === selectedId.value)) selectedId.value = baselines.value[0]?.id ?? ''
  }

  async function loadKinds() {
    const k = await guard(() => callApp('BaselineKinds'))
    if (k) kinds.value = k as string[]
  }

  async function loadDetails(id = selectedId.value) {
    if (!id) return
    selectedId.value = id
    const [v, r, e] = await Promise.all([
      guard(() => callApp('BaselineVersions', id, '', PAGE_SIZE)),
      guard(() => callApp('DriftResults', id, '', PAGE_SIZE)),
      guard(() => callApp('BaselineExceptions', id)),
    ])
    if (selectedId.value !== id) return // пользователь уже выбрал другой baseline
    if (v) { versions.value = (v as any).items ?? []; versionsNext.value = (v as any).next ?? '' }
    if (r) { results.value = (r as any).items ?? []; resultsNext.value = (r as any).next ?? '' }
    if (e) exceptions.value = e as BaselineException[]
    result.value = null
  }

  async function moreVersions() {
    if (!versionsNext.value) return
    const v = await guard(() => callApp('BaselineVersions', selectedId.value, versionsNext.value, PAGE_SIZE))
    if (v) { versions.value.push(...((v as any).items ?? [])); versionsNext.value = (v as any).next ?? '' }
  }

  async function moreResults() {
    if (!resultsNext.value) return
    const r = await guard(() => callApp('DriftResults', selectedId.value, resultsNext.value, PAGE_SIZE))
    if (r) { results.value.push(...((r as any).items ?? [])); resultsNext.value = (r as any).next ?? '' }
  }

  async function check(): Promise<boolean> {
    if (!selectedId.value || checking.value) return false
    checking.value = true
    const res = await guard(() => callApp('DriftCheck', selectedId.value))
    checking.value = false
    if (!res) return false
    result.value = res as BaselineCheckResult
    await loadDetails()
    result.value = res as BaselineCheckResult
    return true
  }

  async function openResult(id: string) {
    const res = await guard(() => callApp('DriftResult', selectedId.value, id))
    if (res) result.value = res as BaselineCheckResult
  }

  async function previewNew(spec: BaselineSpec): Promise<BaselinePreview | undefined> {
    return await guard(() => callApp('BaselinePreviewNew', spec as never)) as BaselinePreview | undefined
  }

  async function previewVersion(): Promise<BaselinePreview | undefined> {
    return await guard(() => callApp('BaselinePreviewVersion', selectedId.value)) as BaselinePreview | undefined
  }

  /** Создание только с подтверждением и комментарием; без них бэкенд ничего не создаёт */
  async function create(name: string, spec: BaselineSpec, comment: string, token: string, confirmed: boolean): Promise<boolean> {
    const b = await guard(() => callApp('BaselineCreate', { name, spec, comment, token, confirmed } as never))
    if (!b) return false
    await loadList()
    await loadDetails((b as Baseline).id)
    return true
  }

  async function addVersion(comment: string, token: string, confirmed: boolean): Promise<boolean> {
    const v = await guard(() => callApp('BaselineAddVersion', { id: selectedId.value, comment, token, confirmed } as never))
    if (!v) return false
    await loadList()
    await loadDetails()
    return true
  }

  async function setActive(versionId: string): Promise<boolean> {
    const b = await guard(() => callApp('BaselineSetActive', selectedId.value, versionId))
    if (!b) return false
    await loadList()
    await loadDetails()
    return true
  }

  async function addException(input: { scope: 'resource' | 'kind'; ref: BaselineRef; path: string; comment: string; expiresAt?: string }): Promise<boolean> {
    const e = await guard(() => callApp('BaselineAddException', selectedId.value, input as never))
    if (!e) return false
    exceptions.value = await callApp('BaselineExceptions', selectedId.value) as BaselineException[]
    return true
  }

  async function removeException(id: string): Promise<boolean> {
    const ok = await guard(async () => { await callApp('BaselineRemoveException', selectedId.value, id); return true })
    if (!ok) return false
    exceptions.value = exceptions.value.filter(e => e.id !== id)
    return true
  }

  async function remove(): Promise<boolean> {
    const id = selectedId.value
    const ok = await guard(async () => { await callApp('BaselineDelete', id); return true })
    if (!ok) return false
    selectedId.value = ''
    result.value = null
    await loadList()
    await loadDetails()
    return true
  }

  return {
    baselines, selectedId, selected, activeVersion, versions, versionsNext, results, resultsNext, exceptions, result, kinds,
    loading, checking, error,
    loadList, loadKinds, loadDetails, moreVersions, moreResults, check, openResult,
    previewNew, previewVersion, create, addVersion, setActive, addException, removeException, remove,
  }
}

export type BaselineStore = ReturnType<typeof useBaseline>
