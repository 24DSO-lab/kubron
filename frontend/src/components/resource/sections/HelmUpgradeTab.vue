<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="hu">
    <div v-if="!allowed" class="rd-empty">{{ $t('helm.upgrade.noWritePerm') }}</div>
    <div v-else-if="error && !loaded" class="rd-empty">{{ error }}</div>
    <div v-else-if="!loaded" class="rd-empty">{{ $t('common.loading') }}</div>
    <template v-else>
      <p class="rd-muted hu-hint">{{ $t('helm.upgrade.upgradeHint') }}</p>

      <MonacoDiff
        v-show="step === 'edit'"
        :original="original"
        v-model:modified="edited"
        editable
      />
      <template v-if="step === 'preview'">
        <div class="rd-muted hu-hint">{{ $t('helm.upgrade.manifestChanges') }}</div>
        <MonacoDiff :original="manifestOld" :modified="manifestNew" />
      </template>

      <div v-if="stale" class="hu-stale">
        {{ $t('helm.upgrade.stale', { rev: props.resource?.revision, base: baseRevision }) }}
        <button type="button" class="rd-btn" :disabled="busy" @click="load">{{ $t('helm.upgrade.reloadValues') }}</button>
      </div>

      <p v-if="error" class="hu-error">{{ error }}</p>

      <div class="hu-actions">
        <template v-if="step === 'edit'">
          <button class="rd-btn" :disabled="!dirty || busy" @click="preview">{{ busy ? $t('helm.upgrade.applying') : $t('helm.upgrade.previewBtn') }}</button>
          <button class="rd-btn" :disabled="!dirty || busy" @click="edited = original">{{ $t('helm.upgrade.reset') }}</button>
        </template>
        <template v-else>
          <button class="rd-btn" :disabled="busy" @click="step = 'edit'">{{ $t('helm.upgrade.back') }}</button>
          <button class="rd-btn hu-apply" :disabled="busy" @click="apply">{{ busy ? $t('helm.upgrade.applying') : $t('helm.upgrade.applyBtn') }}</button>
        </template>
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import MonacoDiff from '../MonacoDiff.vue'
import { callApp } from '../../../resources/wails'
import { runHelmOp } from '../../../composables/useHelmOps'
import { askConfirm } from '../../../resources/confirm'
import { withFluxWarning } from './HelmFluxGuard'
import { reloadActive } from '../../../resources/store'
import { itemName, itemNs } from '../../../resources/utils'
import { can } from '../../../composables/useAuth'
import { useNotifications } from '../../../composables/useNotifications'
import { Perm } from '../../../types/auth'
import { errorText } from '../../../composables/errorText'

const { t } = useI18n()
const props = defineProps<{ resource: any }>()
const notifications = useNotifications()

const ns = computed(() => itemNs(props.resource))
const name = computed(() => itemName(props.resource))
const allowed = computed(() => can(Perm.Write) && can(Perm.SecretsRead))

const step = ref<'edit' | 'preview'>('edit')
const loaded = ref(false)
const busy = ref(false)
const error = ref('')
const original = ref('')
const edited = ref('')
const manifestOld = ref('')
const manifestNew = ref('')
const dirty = computed(() => edited.value !== original.value)
const baseRevision = ref<number | undefined>(undefined) // ревизия, на которой основан редактор
const stale = ref(false)

let seq = 0
async function load() {
  const my = ++seq
  loaded.value = false
  error.value = ''
  step.value = 'edit'
  stale.value = false
  if (!allowed.value) return
  try {
    // revision 0 — последняя; в кеш такой запрос не попадает
    const c = await callApp('GetHelmReleaseContent', ns.value, name.value, 0)
    if (my !== seq) return
    original.value = c.values ?? ''
    edited.value = original.value
    baseRevision.value = props.resource?.revision
    loaded.value = true
  } catch (e: any) {
    if (my === seq) error.value = errorText(e)
  }
}
// Релиз сменился или появилась новая ревизия: перечитываем исходные values
watch(() => [ns.value, name.value], load, { immediate: true })
// Ревизия изменилась (live-обновление): без правок тихо перечитываем, с правками предупреждаем и ничего не затираем
watch(() => props.resource?.revision, rev => {
  if (!loaded.value || rev === baseRevision.value) return
  if (dirty.value || busy.value) stale.value = true
  else load()
})

async function preview() {
  busy.value = true
  error.value = ''
  try {
    const p = await callApp('PreviewHelmUpgrade', ns.value, name.value, edited.value)
    manifestOld.value = p.currentManifest
    manifestNew.value = p.newManifest
    step.value = 'preview'
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

async function apply() {
  const message = withFluxWarning('upgrade', props.resource, name.value, t('helm.upgrade.confirmUpgrade', { name: name.value }))
  if (!(await askConfirm(message))) return
  busy.value = true
  error.value = ''
  try {
    const rev = await runHelmOp({ op: 'upgrade', namespace: ns.value, release: name.value, cancelable: true }, id =>
      callApp('UpgradeHelmRelease', ns.value, name.value, edited.value, id))
    notifications.notifySuccess(t('helm.upgrade.upgraded', { name: name.value, rev }))
    await reloadActive()
    await load()
    baseRevision.value = rev // собственная ревизия не считается чужим изменением
    stale.value = false
  } catch (e: any) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.hu-hint { font-size: 0.85rem; margin: 0 0 8px; }
.hu-stale { padding: 8px 10px; border: 1px solid var(--warn); border-radius: 6px; color: var(--warn); font-size: 0.85rem; display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-top: 8px; }
.hu-actions { display: flex; gap: 8px; margin-top: 10px; }
.hu-actions .rd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.hu-error { color: var(--red-soft); font-size: 0.85rem; margin: 8px 0 0; white-space: pre-wrap; }
</style>
