<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="history.length">
    <div class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr><th>{{ $t('defs.columns.revision') }}</th><th>{{ $t('common.status') }}</th><th>{{ $t('defs.props.chart') }}</th><th>{{ $t('defs.columns.appVersion') }}</th><th>{{ $t('defs.columns.updated') }}</th><th>{{ $t('defs.props.description') }}</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="h in history" :key="h.revision">
            <td class="rd-mono">#{{ h.revision }}<span v-if="h.revision === current" class="rd-muted"> ({{ $t('helm.history.current') }})</span></td>
            <td><span class="rd-badge" :class="badgeClass(h.status)">{{ h.status }}</span></td>
            <td class="rd-mono">{{ h.chart }}-{{ h.chartVersion }}</td>
            <td class="rd-mono">{{ h.appVersion || '—' }}</td>
            <td class="rd-muted" :title="h.updatedAt">{{ h.updated || '—' }}</td>
            <td class="rd-muted">{{ h.description || '—' }}</td>
            <td class="hh-actions">
              <template v-if="h.revision !== current">
                <button v-if="can(Perm.SecretsRead)" class="rd-btn" @click="showDiff(h.revision)">{{ $t('helm.history.diff') }}</button>
                <span v-else class="rd-muted" :title="$t('helm.history.diffNoAccess')">{{ $t('helm.history.noAccess') }}</span>
                <button v-if="can(Perm.Operate)" class="rd-btn" :disabled="busy" @click="rollback(h.revision)">{{ $t('helm.history.rollback') }}</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="diffRev !== null" class="hh-diff">
      <div class="hh-bar">
        <span class="rd-mono">#{{ diffRev }} → #{{ current }}</span>
        <div class="hh-seg" role="group" :aria-label="$t('helm.history.diffTarget')">
          <button :class="{ on: part === 'manifest' }" @click="part = 'manifest'">{{ $t('helm.history.manifest') }}</button>
          <button :class="{ on: part === 'values' }" @click="part = 'values'">{{ $t('helm.history.values') }}</button>
        </div>
        <button class="rd-btn hh-close" @click="closeDiff">{{ $t('helm.history.close') }}</button>
      </div>
      <p v-if="diffError" class="hh-error">{{ diffError }}</p>
      <p v-else-if="diffLoading" class="rd-muted">{{ $t('helm.history.loadingDiff') }}</p>
      <p v-else-if="!text" class="rd-muted">{{ $t('helm.history.noDifferences') }}</p>
      <pre v-else class="hh-pre"><span v-for="(l, i) in lines" :key="i" :class="lineClass(l)">{{ l }}</span></pre>
    </div>
  </div>
  <div v-else class="rd-empty">{{ $t('helm.history.empty') }}</div>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { badgeClass } from '../../../resources/status'
import { callApp } from '../../../resources/wails'
import { runHelmOp } from '../../../composables/useHelmOps'
import { askConfirm } from '../../../resources/confirm'
import { reloadActive } from '../../../resources/store'
import { itemName, itemNs } from '../../../resources/utils'
import { can } from '../../../composables/useAuth'
import { useNotifications } from '../../../composables/useNotifications'
import { Perm } from '../../../types/auth'
import type { k8s } from '../../../../wailsjs/go/models'
import { errorText } from '../../../composables/errorText'

const props = defineProps<{ resource: any }>()
const { t } = useI18n()
const notifications = useNotifications()

const history = computed<any[]>(() => props.resource?.history ?? [])
const current = computed(() => Number(props.resource?.revision) || 0)
const ns = computed(() => itemNs(props.resource))
const name = computed(() => itemName(props.resource))

const busy = ref(false)

async function rollback(revision: number) {
  if (!(await askConfirm(t('helm.history.confirmRollback', { name: name.value, revision })))) return
  busy.value = true
  try {
    await runHelmOp({ op: 'rollback', namespace: ns.value, release: name.value }, id =>
      callApp('RollbackHelmRelease', ns.value, name.value, revision, id))
    notifications.notifySuccess(t('helm.history.rolledBack', { name: name.value, revision }))
    await reloadActive()
  } catch (e: any) {
    notifications.notifyError(t('helm.history.rollbackError', { message: errorText(e) }))
  } finally {
    busy.value = false
  }
}

// ─── Diff ───
const diffRev = ref<number | null>(null)
const diff = ref<k8s.HelmRevisionDiff | null>(null)
const diffLoading = ref(false)
const diffError = ref('')
const part = ref<'manifest' | 'values'>('manifest')

let seq = 0
async function showDiff(revision: number) {
  const my = ++seq
  diffRev.value = revision
  diff.value = null
  diffError.value = ''
  diffLoading.value = true
  try {
    const d = await callApp('GetHelmRevisionDiff', ns.value, name.value, revision, current.value)
    if (my !== seq) return
    diff.value = d
  } catch (e: any) {
    if (my !== seq) return
    diffError.value = errorText(e)
  } finally {
    if (my === seq) diffLoading.value = false
  }
}

function closeDiff() {
  seq++
  diffRev.value = null
  diff.value = null
  diffError.value = ''
}

// Открыт другой релиз: старый diff к нему не относится
watch(() => `${ns.value}/${name.value}`, closeDiff)

const text = computed(() => (diff.value ? diff.value[part.value] : ''))
const lines = computed(() => text.value.split('\n'))
const lineClass = (l: string) =>
  l.startsWith('@@') ? 'hh-hunk' : l.startsWith('+') ? 'hh-add' : l.startsWith('-') ? 'hh-del' : 'hh-ctx'
</script>

<style scoped>
.hh-actions { display: flex; gap: 6px; justify-content: flex-end; white-space: nowrap; }
.hh-actions .rd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.hh-diff { margin-top: 12px; }
.hh-bar { display: flex; align-items: center; gap: 12px; padding: 8px 0; flex-wrap: wrap; font-size: 0.85rem; }
.hh-seg { display: inline-flex; border: 1px solid #3b3d5c; border-radius: 4px; overflow: hidden; }
.hh-seg button { padding: 4px 10px; background: transparent; color: #9ca3af; border: 0; font: inherit; cursor: pointer; }
.hh-seg button.on { background: #3b82f6; color: #fff; }
.hh-close { margin-left: auto; }
.hh-error { color: #f87171; font-size: 0.85rem; margin: 8px 0; }
.hh-pre {
  margin: 0; padding: 8px 10px; overflow: auto; max-height: 420px; background: #12141f;
  border: 1px solid #2a2f47; border-radius: 4px; font-family: 'Courier New', monospace; font-size: 0.8rem; line-height: 1.45; text-align: left;
}
.hh-pre span { display: block; white-space: pre; }
.hh-add { color: #4ade80; background: rgba(74, 222, 128, 0.08); }
.hh-del { color: #f87171; background: rgba(248, 113, 113, 0.08); }
.hh-hunk { color: #60a5fa; }
.hh-ctx { color: #9ca3af; }
</style>