<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <ResourceDetailPanel
    v-if="def && detail"
    :key="`${def.id}/${opened?.ns}/${opened?.name}`"
    :def="def"
    :resource="detail"
    :ns="opened!.ns"
    :name="opened!.name"
    @close="resourceActions.closeDetail()"
  />

  <ScaleDialog
    v-if="scaleTarget"
    :deployment="scaleTarget"
    @close="resourceActions.cancelScale()"
    @scale="resourceActions.confirmScale"
  />

    <SyncDialog v-if="pendingArgoSync" :apps="pendingArgoSync.apps" @close="closeArgoSync()" />

    <DeleteAppDialog
    v-if="pendingArgoDelete"
    :app="pendingArgoDelete"
    @close="closeArgoDelete()"
    @deleted="resourceActions.closeDetail()"
  />

  <FluxDeleteDialog
    v-if="pendingFluxDelete"
    :object="pendingFluxDelete"
    @close="closeFluxDelete()"
    @deleted="resourceActions.closeDetail()"
  />

  <!-- Подтверждение действий (ctx.confirm): Restart, Evict, Rollback ... -->
  <ConfirmationDialog
    :model-value="!!pendingConfirm"
    resource-type=""
    resource-name=""
    :title="pendingConfirm?.message"
    @confirm="answerConfirm(true)"
    @cancel="answerConfirm(false)"
  />

  <!-- Массовое удаление: чекбоксы в списке + «−» -->
  <ConfirmationDialog
    :model-value="!!pendingBulkDelete"
    :resource-type="pendingBulkDelete?.kind ?? ''"
    resource-name=""
    :title="bulkTitle"
    :items="bulkItems"
    @confirm="resourceActions.confirmBulkDelete()"
    @cancel="resourceActions.cancelBulkDelete()"
  />

  <ConfirmationDialog
    v-model="confirmOpen"
    :resource-type="pendingDelete?.kind ?? ''"
    :resource-name="pendingDelete?.name ?? ''"
    :namespace="pendingDelete?.ns ?? ''"
    @confirm="resourceActions.confirmDelete()"
    @cancel="resourceActions.cancelDelete()"
  />
</template>

<script lang="ts" setup>
/**
 * Монтируется в App.vue один раз. Рисует единственную панель деталей, диалог масштабирования
 * и диалоги подтверждения (удаление и ctx.confirm) на всё приложение.
 */
import { computed, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import ResourceDetailPanel from './ResourceDetailPanel.vue'
import ConfirmationDialog from '../utils/ConfirmationDialog.vue'
import ScaleDialog from '../panels/ScaleDialog.vue'
import { getResourceById } from '../../resources/registry'
import { findLoaded, reloadActive } from '../../resources/store'
import { itemName, itemNs } from '../../resources/utils'
import { opened, pendingDelete, pendingBulkDelete, scaleTarget, resourceActions } from '../../resources/actions'
import { pendingConfirm, answerConfirm } from '../../resources/confirm'
import SyncDialog from '../panels/SyncDialog.vue'
import { pendingArgoSync, closeArgoSync } from '../../resources/argoActions'
import DeleteAppDialog from '../panels/DeleteAppDialog.vue'
import { pendingArgoDelete, closeArgoDelete } from '../../resources/argoActions'
import FluxDeleteDialog from '../panels/FluxDeleteDialog.vue'
import { pendingFluxDelete, closeFluxDelete } from '../../resources/fluxActions'

const { t } = useI18n()

const def = computed(() => (opened.value ? getResourceById(opened.value.id) : undefined))
const detail = shallowRef<any>(null)

watch(
  opened,
  async o => {
    detail.value = null
    if (!o) return
    const d = getResourceById(o.id)
    if (!d) {
      resourceActions.notifyError(t('resource.host.unknownType', { id: o.id }))
      resourceActions.closeDetail()
      return
    }
    const stale = () => opened.value !== o
    let failure: any = null

    // 1) сразу показываем то, что уже есть (элемент списка)
    detail.value = o.item ?? findLoaded(d.view, o.ns, o.name) ?? null

    // 2) api.get отдаёт более полные данные
    if (d.api.get) {
      try {
        const fresh = await d.api.get(o.ns, o.name)
        if (stale()) return
        // get может вернуть лишь часть полей (PodDetail): дополняем элемент списка, а не заменяем его
        if (fresh) detail.value = { ...(detail.value ?? {}), ...fresh }
      } catch (e) {
        failure = e
      }
    }

    // 3) ресурс открыт по ссылке (по имени), а в кэше его нет: ищем в списке
    if (!detail.value) {
      try {
        const items = await d.api.list(d.namespaced ? [o.ns] : [])
        if (stale()) return
        detail.value =
          items?.find((x: any) => itemName(x) === o.name && (!d.namespaced || itemNs(x) === o.ns)) ?? null
      } catch (e) {
        failure = failure ?? e
      }
    }

    if (stale()) return
    if (!detail.value) {
      resourceActions.notifyError(
        failure
          ? `${d.kind}: ${errorText(failure)}`
          : t('resource.host.notFound', { kind: d.kind, name: o.name }),
      )
      resourceActions.closeDetail()
    }
  },
  { immediate: true },
)

// Live-обновление открытой панели: срабатывает вместе с refreshOn определения (если liveDetail: true)
let refetchSeq = 0
watch(
  () => (def.value?.liveDetail ? def.value.refreshOn?.() : undefined),
  async () => {
    const o = opened.value
    const d = def.value
    if (!o || !d?.liveDetail || !d.api.get || !detail.value) return
    const my = ++refetchSeq
    const stale = () => my !== refetchSeq || opened.value !== o
    try {
      await reloadActive() // свежие поля элемента списка (ревизия, статус)
      const fresh = await d.api.get(o.ns, o.name)
      if (stale()) return
      const row = findLoaded(d.view, o.ns, o.name)
      detail.value = { ...detail.value, ...(row ?? {}), ...(fresh ?? {}) }
    } catch {
      // ресурс мог быть удалён: список обновится сам, панель не закрываем принудительно
    }
  },
)

const bulkTitle = computed(() => {
  const p = pendingBulkDelete.value
  return p ? t('confirm.bulkDelete', { n: p.items.length, kind: p.kind }, p.items.length) : ''
})
const bulkItems = computed(() =>
  (pendingBulkDelete.value?.items ?? []).map(i => (i.ns ? `${i.ns}/${i.name}` : i.name)),
)

// Модель диалога удаления синхронизируется с pendingDelete; чистят её только confirm/cancel
const confirmOpen = ref(false)
watch(pendingDelete, v => { confirmOpen.value = !!v })
</script>
