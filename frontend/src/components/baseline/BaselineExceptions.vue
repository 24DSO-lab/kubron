<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="bx" :aria-label="$t('baseline.exceptions.title')">
    <h3>{{ $t('baseline.exceptions.title') }}</h3>
    <p v-if="!store.exceptions.value.length" class="bx-muted">{{ $t('baseline.exceptions.none') }}</p>
    <table v-else class="bx-table">
      <tbody>
        <tr v-for="e in store.exceptions.value" :key="e.id">
          <td>
            <strong>{{ e.scope === 'kind' ? e.ref.kind : refLabel(e.ref) }}</strong>
            <code class="bx-path">{{ e.path }}</code>
            <div class="bx-muted">{{ e.comment }}</div>
            <div class="bx-muted">
              {{ $t('baseline.exceptions.createdBy', { user: e.createdBy }) }} ·
              <span :class="{ bad: expired(e) }">{{ e.expiresAt ? (expired(e) ? $t('baseline.exceptions.expired') + ' ' : '') + formatDateTime(e.expiresAt) : $t('baseline.exceptions.never') }}</span>
            </div>
          </td>
          <td class="bx-act">
            <button v-if="canManage" class="btn" type="button" @click="remove(e.id)">{{ $t('baseline.exceptions.remove') }}</button>
          </td>
        </tr>
      </tbody>
    </table>

    <form v-if="canManage" class="bx-form" @submit.prevent="add">
      <label class="bx-field">{{ $t('baseline.exceptions.scope') }}
        <select v-model="scope" class="form-input">
          <option value="resource">{{ $t('baseline.exceptions.scopeResource') }}</option>
          <option value="kind">{{ $t('baseline.exceptions.scopeKind') }}</option>
        </select>
      </label>
      <label class="bx-field">{{ $t('baseline.exceptions.kind') }}
        <input v-model.trim="kind" class="form-input" required autocomplete="off" spellcheck="false" />
      </label>
      <label class="bx-field">{{ $t('baseline.exceptions.namespace') }}
        <input v-model.trim="namespace" class="form-input" autocomplete="off" spellcheck="false" />
      </label>
      <label v-if="scope === 'resource'" class="bx-field">{{ $t('baseline.exceptions.name') }}
        <input v-model.trim="name" class="form-input" required autocomplete="off" spellcheck="false" />
      </label>
      <label class="bx-field">{{ $t('baseline.exceptions.path') }}
        <input v-model.trim="path" class="form-input" required maxlength="512" autocomplete="off" spellcheck="false" :placeholder="$t('baseline.exceptions.pathHint')" />
      </label>
      <label class="bx-field">{{ $t('baseline.exceptions.expires') }}
        <input v-model="expires" class="form-input" type="date" />
      </label>
      <label class="bx-field bx-wide">{{ $t('baseline.exceptions.comment') }}
        <input v-model.trim="comment" class="form-input" required maxlength="2000" autocomplete="off" />
      </label>
      <button class="btn btn-primary" type="submit" :disabled="!valid">{{ $t('baseline.exceptions.add') }}</button>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { t } from '../../i18n'
import { formatDateTime } from '../../i18n/format'
import { askConfirm } from '../../resources/confirm'
import { refLabel, type BaselineStore } from '../../composables/useBaseline'
import type { BaselineException, BaselineRef } from '../../types/baseline'

const props = defineProps<{ store: BaselineStore; canManage: boolean; prefill: { ref: BaselineRef; path: string } | null }>()

const scope = ref<'resource' | 'kind'>('resource')
const kind = ref('')
const namespace = ref('')
const name = ref('')
const path = ref('')
const expires = ref('')
const comment = ref('')

watch(() => props.prefill, p => {
  if (!p) return
  scope.value = 'resource'
  kind.value = p.ref.kind
  namespace.value = p.ref.namespace ?? ''
  name.value = p.ref.name
  path.value = p.path
}, { immediate: true })

const valid = computed(() =>
  kind.value !== '' && path.value !== '' && comment.value !== '' && (scope.value === 'kind' || name.value !== ''))

const expired = (e: BaselineException) => !!e.expiresAt && new Date(e.expiresAt).getTime() < Date.now()

/** Версия API ресурса для исключения не важна: сопоставление идёт по группе, типу, пространству и имени */
async function add() {
  const group = props.prefill && props.prefill.ref.kind === kind.value ? props.prefill.ref.group ?? '' : ''
  const ok = await props.store.addException({
    scope: scope.value,
    ref: { group, version: 'v1', kind: kind.value, namespace: namespace.value, name: scope.value === 'resource' ? name.value : '' },
    path: path.value,
    comment: comment.value,
    // дата без времени → конец этого дня по UTC
    expiresAt: expires.value ? `${expires.value}T23:59:59Z` : undefined,
  })
  if (ok) { path.value = ''; comment.value = ''; expires.value = '' }
}

async function remove(id: string) {
  if (!(await askConfirm(t('baseline.exceptions.confirmRemove')))) return
  await props.store.removeException(id)
}
</script>

<style scoped>
.bx { display: flex; flex-direction: column; gap: 10px; }
.bx h3 { margin: 0; font-size: 15px; }
.bx-muted { color: #6c7086; font-size: 12px; }
.bx-muted .bad { color: #f38ba8; }
.bx-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.bx-table td { padding: 8px 10px; border-bottom: 1px solid #232334; vertical-align: top; }
.bx-path { margin-left: 8px; color: #cdd6f4; }
.bx-act { width: 1%; white-space: nowrap; }
.bx-form { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 10px; align-items: end; }
.bx-field { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: #a6adc8; }
.bx-wide { grid-column: 1 / -1; }
</style>
