<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="bv">
    <header class="bv-head">
      <div>
        <h2>{{ $t('baseline.title') }}</h2>
        <p class="bv-sub">{{ $t('baseline.subtitle') }}</p>
      </div>
      <div class="bv-actions">
        <button class="btn" type="button" :disabled="store.loading.value" @click="reload">{{ $t('baseline.refresh') }}</button>
        <button v-if="canManage" class="btn btn-primary" type="button" @click="dialog = 'new'">{{ $t('baseline.new') }}</button>
      </div>
    </header>

    <p v-if="store.error.value" class="form-error" role="alert">{{ store.error.value }}</p>
    <p v-if="!canManage" class="bv-muted">{{ $t('baseline.noManage') }}</p>

    <p v-if="loaded && !store.baselines.value.length" class="bv-empty">{{ $t('baseline.empty') }}</p>

    <template v-if="store.selected.value">
      <section class="bv-card">
        <div class="bv-pick">
          <label class="bv-field">{{ $t('baseline.select') }}
            <select class="form-input" :value="store.selectedId.value" @change="select(($event.target as HTMLSelectElement).value)">
              <option v-for="b in store.baselines.value" :key="b.id" :value="b.id">{{ b.name }}</option>
            </select>
          </label>
          <div class="bv-meta">
            <div v-if="store.activeVersion.value">
              {{ $t('baseline.activeVersion') }}: <strong>{{ $t('baseline.version', { seq: store.activeVersion.value.seq }) }}</strong>
              · {{ $t('baseline.objects', { count: store.activeVersion.value.objectCount }) }}
              · {{ formatDateTime(store.activeVersion.value.createdAt) }}
            </div>
            <div class="bv-muted">
              {{ $t('baseline.scope') }}:
              <template v-if="store.selected.value.refs.length">{{ $t('baseline.scopeRefs', { count: store.selected.value.refs.length }) }}</template>
              <template v-else>{{ $t('baseline.scopeNamespaces', { list: store.selected.value.namespaces.join(', ') }) }}</template>
              <template v-if="store.selected.value.selector"> · {{ store.selected.value.selector }}</template>
            </div>
          </div>
        </div>
        <div class="bv-actions">
          <button class="btn btn-primary" type="button" :disabled="store.checking.value || !store.selected.value.activeVersionId" @click="store.check()">
            {{ store.checking.value ? $t('baseline.checking') : $t('baseline.check') }}
          </button>
          <button v-if="canManage" class="btn" type="button" @click="dialog = 'version'">{{ $t('baseline.newVersion') }}</button>
          <button v-if="canManage" class="btn bv-danger" type="button" @click="remove">{{ $t('baseline.delete') }}</button>
        </div>
      </section>

      <section class="bv-card">
        <BaselineCheckResult v-if="store.result.value" :result="store.result.value" :can-manage="canManage" @add-exception="onAddException" />
        <p v-else class="bv-muted">{{ $t('baseline.result.none') }}</p>
      </section>

      <section class="bv-card">
        <BaselineExceptions :store="store" :can-manage="canManage" :prefill="prefill" />
      </section>

      <section class="bv-card">
        <h3>{{ $t('baseline.versions.title') }}</h3>
        <table class="bv-table">
          <thead><tr>
            <th>{{ $t('baseline.versions.seq') }}</th><th>{{ $t('baseline.versions.created') }}</th>
            <th>{{ $t('baseline.versions.author') }}</th><th>{{ $t('baseline.versions.comment') }}</th><th />
          </tr></thead>
          <tbody>
            <tr v-for="v in store.versions.value" :key="v.id">
              <td>{{ v.seq }} <span v-if="v.id === store.selected.value.activeVersionId" class="bv-badge">{{ $t('baseline.versions.active') }}</span></td>
              <td class="bv-muted">{{ formatDateTime(v.createdAt) }}</td>
              <td>{{ v.author }}</td>
              <td class="bv-comment">{{ v.comment }}</td>
              <td class="bv-act">
                <button v-if="canManage && v.id !== store.selected.value.activeVersionId" class="btn" type="button" @click="activate(v.seq, v.id)">{{ $t('baseline.versions.makeActive') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
        <button v-if="store.versionsNext.value" class="btn" type="button" @click="store.moreVersions()">{{ $t('baseline.versions.more') }}</button>
      </section>

      <section class="bv-card">
        <h3>{{ $t('baseline.history.title') }}</h3>
        <p v-if="!store.results.value.length" class="bv-muted">{{ $t('baseline.history.none') }}</p>
        <table v-else class="bv-table">
          <thead><tr><th>{{ $t('baseline.history.time') }}</th><th>{{ $t('baseline.history.initiator') }}</th><th /><th /></tr></thead>
          <tbody>
            <tr v-for="r in store.results.value" :key="r.id">
              <td class="bv-muted">{{ formatDateTime(r.checkedAt) }}</td>
              <td>{{ r.initiator }}</td>
              <td class="bv-muted">{{ $t('baseline.summary.changed', { count: r.summary.changed }) }} · {{ $t('baseline.summary.missing', { count: r.summary.missing }) }} · {{ $t('baseline.summary.unchecked', { count: r.summary.unchecked }) }}</td>
              <td class="bv-act"><button class="btn" type="button" @click="store.openResult(r.id)">{{ $t('baseline.history.open') }}</button></td>
            </tr>
          </tbody>
        </table>
        <button v-if="store.resultsNext.value" class="btn" type="button" @click="store.moreResults()">{{ $t('baseline.history.more') }}</button>
      </section>
    </template>

    <BaselineCreateDialog v-if="dialog" :store="store" :mode="dialog" @close="dialog = null" @done="dialog = null" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { t } from '../../i18n'
import { formatDateTime } from '../../i18n/format'
import { askConfirm } from '../../resources/confirm'
import { can } from '../../composables/useAuth'
import { useBaseline } from '../../composables/useBaseline'
import { Perm } from '../../types/auth'
import type { BaselineRef } from '../../types/baseline'
import BaselineCheckResult from '../baseline/BaselineCheckResult.vue'
import BaselineCreateDialog from '../baseline/BaselineCreateDialog.vue'
import BaselineExceptions from '../baseline/BaselineExceptions.vue'

const store = useBaseline()
const loaded = ref(false)
const dialog = ref<'new' | 'version' | null>(null)
const prefill = ref<{ ref: BaselineRef; path: string } | null>(null)

/** Скрытая кнопка — только удобство: право проверяет бэкенд в каждом методе */
const canManage = computed(() => can(Perm.BaselineManage))

async function reload() {
  await store.loadList()
  await store.loadDetails()
}

async function select(id: string) {
  store.result.value = null
  await store.loadDetails(id)
}

onMounted(async () => {
  await reload()
  loaded.value = true
})

function onAddException(ref: BaselineRef, path: string) {
  prefill.value = { ref, path }
}

async function activate(seq: number, id: string) {
  if (!(await askConfirm(t('baseline.versions.confirmActive', { seq })))) return
  await store.setActive(id)
}

async function remove() {
  const name = store.selected.value?.name ?? ''
  if (!(await askConfirm(t('baseline.confirmDelete', { name })))) return
  await store.remove()
}
</script>

<style scoped>
.bv { display: flex; flex-direction: column; gap: 14px; padding: 16px 20px; }
.bv-head { display: flex; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.bv-head h2 { margin: 0; }
.bv-sub { margin: 4px 0 0; color: #a6adc8; font-size: 13px; max-width: 760px; }
.bv-actions { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.bv-muted { color: #6c7086; font-size: 12px; }
.bv-empty { color: #a6adc8; }
.bv-card { background: #1e1e2e; border: 1px solid #313244; border-radius: 8px; padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; }
.bv-card h3 { margin: 0; font-size: 15px; }
.bv-pick { display: flex; gap: 20px; flex-wrap: wrap; align-items: flex-end; }
.bv-field { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: #a6adc8; min-width: 220px; }
.bv-meta { font-size: 13px; display: flex; flex-direction: column; gap: 2px; }
.bv-danger { color: #f38ba8; }
.bv-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.bv-table th { text-align: left; padding: 6px 10px; color: #a6adc8; font-weight: 600; border-bottom: 1px solid #313244; }
.bv-table td { padding: 6px 10px; border-bottom: 1px solid #232334; vertical-align: top; }
.bv-comment { max-width: 420px; word-break: break-word; }
.bv-act { width: 1%; white-space: nowrap; }
.bv-badge { padding: 1px 8px; border-radius: 10px; font-size: 12px; color: #a6e3a1; background: rgba(166, 227, 161, 0.12); }
</style>
