// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, onMounted, onUnmounted, ref, shallowRef, watch, type Ref } from 'vue'
import { callApp } from '../resources/wails'
import { fluxActionTick } from '../resources/fluxActions'
import type { FluxHelmReleaseInfo, FluxKustomizationInfo, FluxStatus } from '../types/flux'
import { errorText } from './errorText'
import { useFlux } from './useFlux'
import { fluxLiveTick } from './useFluxLive'

// Основное обновление приходит событием flux:changed; таймер — подстраховка (informer мог не стартовать)
const REFRESH_MS = 60_000

/** Результат одной загрузки: список по каждому виду или причина, по которой его получить не удалось */
export interface FluxLoadResult {
  kustomizations: FluxKustomizationInfo[] | null
  helmReleases: FluxHelmReleaseInfo[] | null
  /** тексты ошибок по видам; пусто, если всё загрузилось */
  errors: string[]
}

/**
 * Читает Kustomization и HelmRelease из всех namespace. Вид без контроллера не запрашивается; при
 * unsupportedVersion объекты не разбираются (ADR-0023, раздел 3). Ошибка одного вида не отменяет другой.
 * `null` в поле результата — «этот вид не удалось прочитать, оставь прежние данные».
 */
export async function loadFluxObjects(status: FluxStatus): Promise<FluxLoadResult> {
  const errors: string[] = []
  const none: FluxLoadResult = { kustomizations: [], helmReleases: [], errors }
  if (!status.installed || status.state === 'unsupportedVersion') return none

  const read = async <T>(enabled: boolean, method: 'GetFluxKustomizations' | 'GetFluxHelmReleases'): Promise<T[] | null> => {
    if (!enabled) return []
    try {
      return ((await callApp(method, [])) as unknown as T[] | null) ?? []
    } catch (e) {
      errors.push(errorText(e))
      return null
    }
  }
  const [kustomizations, helmReleases] = await Promise.all([
    read<FluxKustomizationInfo>(status.controllers?.kustomize === true, 'GetFluxKustomizations'),
    read<FluxHelmReleaseInfo>(status.controllers?.helm === true, 'GetFluxHelmReleases'),
  ])
  return { kustomizations, helmReleases, errors }
}

/**
 * Kustomization и HelmRelease из всех namespace для дашборда Flux.
 * Контракт тот же, что у useArgoApplications: устаревший ответ отбрасывается, при ошибке остаются последние данные,
 * пока вкладка скрыта, таймер не запрашивает.
 */
export function useFluxObjects(clusterName: Ref<string>) {
  const { fluxStatus } = useFlux()
  const kustomizations = shallowRef<FluxKustomizationInfo[] | null>(null)
  const helmReleases = shallowRef<FluxHelmReleaseInfo[] | null>(null)
  const loading = ref(true)
  const refreshing = ref(false)
  const error = ref('')
  const updatedAt = ref<Date | null>(null)

  /** null, пока ничего не загружено: отличает «загрузка» и «ошибка» от «пусто» */
  const objects = computed(() =>
    kustomizations.value === null && helmReleases.value === null
      ? null
      : [...(kustomizations.value ?? []), ...(helmReleases.value ?? [])],
  )

  let seq = 0
  let timer: ReturnType<typeof setInterval> | null = null

  async function refresh() {
    const mySeq = ++seq
    refreshing.value = true
    try {
      const res = await loadFluxObjects(fluxStatus.value)
      if (mySeq !== seq) return
      // вид, который не удалось прочитать, остаётся с прежними данными
      if (res.kustomizations) kustomizations.value = res.kustomizations
      if (res.helmReleases) helmReleases.value = res.helmReleases
      error.value = res.errors.join('; ')
      if (!res.errors.length) updatedAt.value = new Date()
    } catch (e) {
      if (mySeq !== seq) return
      error.value = errorText(e)
    } finally {
      if (mySeq === seq) {
        loading.value = false
        refreshing.value = false
      }
    }
  }

  const onVisibility = () => { if (!document.hidden) void refresh() }

  onMounted(() => {
    void refresh()
    timer = setInterval(() => { if (!document.hidden) void refresh() }, REFRESH_MS)
    document.addEventListener('visibilitychange', onVisibility)
  })
  onUnmounted(() => {
    seq++
    if (timer) clearInterval(timer)
    document.removeEventListener('visibilitychange', onVisibility)
  })

  watch(clusterName, () => {
    kustomizations.value = null
    helmReleases.value = null
    error.value = ''
    loading.value = true
    void refresh()
  })
  watch(fluxActionTick, () => { void refresh() })
  watch(fluxLiveTick, () => { void refresh() })
  // появился или пропал контроллер, обновился статус версии
  watch(() => [fluxStatus.value.installed, fluxStatus.value.state, fluxStatus.value.controllers.kustomize, fluxStatus.value.controllers.helm], () => { void refresh() })

  return { objects, kustomizations, helmReleases, loading, refreshing, error, updatedAt, refresh }
}
