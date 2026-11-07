// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { ref, shallowRef } from 'vue'
import type { FluxKustomizationInfo, FluxHelmReleaseInfo, FluxObjectInfo } from '../types/flux'

/**
 * Заглушка задачи 0.2: действия над объектами Flux заполняет задача U.10. Форма экспортов зафиксирована:
 * определения ресурсов и ResourceHost импортируют их напрямую (не через actions.ts, чтобы не получить цикл
 * registry -> definitions -> actions), как и argoActions.ts.
 */

/** Растёт после каждого действия: дашборд по нему перечитывает данные */
export const fluxActionTick = ref(0)

/** Объект, для которого открыт диалог удаления (рисует ResourceHost); удалять можно только Kustomization и HelmRelease */
export const pendingFluxDelete = shallowRef<FluxKustomizationInfo | FluxHelmReleaseInfo | null>(null)
export const requestFluxDelete = (obj: FluxKustomizationInfo | FluxHelmReleaseInfo) => { pendingFluxDelete.value = obj }
export const closeFluxDelete = () => { pendingFluxDelete.value = null }

/** reconcile объектов; withSource — вместе с источником (U.10) */
export async function reconcileFlux(_items: FluxObjectInfo[], _opts: { withSource: boolean }): Promise<void> {}

/** suspend (true) и resume (false) объектов (U.10) */
export async function setFluxSuspended(_items: FluxObjectInfo[], _suspended: boolean): Promise<void> {}
