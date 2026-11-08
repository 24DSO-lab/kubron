// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { t } from '../../../i18n'

/**
 * Предупреждение перед ручным действием над релизом Helm, которым управляет Flux (ADR-0023, раздел 9).
 * Бэкенд помечает такие релизы полем fluxManagedBy (GetHelmReleases); ручной upgrade, rollback или uninstall
 * расходится с Git, и helm-controller при следующем reconcile вернёт состояние из HelmRelease.
 * Предупреждение добавляется в текст существующего подтверждения: продолжить можно тем же «Подтвердить».
 * Модуль без Vue и Wails: чистая функция над данными релиза.
 */

export type HelmFluxAction = 'upgrade' | 'rollback' | 'uninstall'

/** HelmRelease, управляющий релизом */
export interface HelmFluxOwner {
  namespace: string
  name: string
}

// Только ключи: перевод вычисляется в withFluxWarning, а не при загрузке модуля (язык выбирается позже).
const WARNING_KEYS: Record<HelmFluxAction, string> = {
  upgrade: 'helm.flux.warnUpgrade',
  rollback: 'helm.flux.warnRollback',
  uninstall: 'helm.flux.warnUninstall',
}

/** HelmRelease, который управляет релизом; null — релиз не под Flux (или ответ неполный) */
export function helmFluxOwner(release: unknown): HelmFluxOwner | null {
  const ref = (release as { fluxManagedBy?: unknown } | null | undefined)?.fluxManagedBy
  if (!ref || typeof ref !== 'object') return null
  const { namespace, name } = ref as { namespace?: unknown; name?: unknown }
  if (typeof name !== 'string' || !name || typeof namespace !== 'string') return null
  return { namespace, name }
}

/** Текст подтверждения: для релиза под Flux — предупреждение, затем исходный вопрос; иначе без изменений */
export function withFluxWarning(action: HelmFluxAction, release: unknown, releaseName: string, message: string): string {
  const owner = helmFluxOwner(release)
  if (!owner) return message
  const warning = t(WARNING_KEYS[action], { name: releaseName, fluxNamespace: owner.namespace, fluxName: owner.name })
  return `${warning} ${message}`
}
