<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="wh">
    <div class="wh-title">{{ $t('defs.columns.webhooks') }}</div>
    <div v-if="!webhooks.length" class="rd-muted">{{ $t('sections.webhooks.empty') }}</div>

    <div v-for="(w, i) in webhooks" :key="w.name + i" class="rd-block">
      <div class="rd-block-head" @click="toggle(i)">
        <span class="wh-name">{{ w.name }}</span>
        <span v-if="w.failurePolicy" class="rd-badge" :class="w.failurePolicy === 'Fail' ? 'rd-warn' : 'rd-unk'">
          {{ w.failurePolicy }}
        </span>
        <span class="rd-arrow" :class="{ open: isOpen(i) }">›</span>
      </div>

      <div v-if="isOpen(i)" class="rd-block-body wh-body">
        <div class="rd-row">
          <span class="rd-label">{{ $t('sections.webhooks.client') }}</span>
          <span class="rd-value">
            <template v-if="w.serviceName">
              <a class="rd-link" @click="openService(w)">{{ w.serviceNamespace }}/{{ w.serviceName }}</a>:{{ w.servicePort
              }}<span v-if="w.servicePath">{{ w.servicePath }}</span>
            </template>
            <template v-else-if="w.clientUrl">{{ w.clientUrl }}</template>
            <template v-else>—</template>
          </span>
        </div>
        <div v-for="f in fields(w)" :key="f.label" class="rd-row">
          <span class="rd-label">{{ f.label }}</span>
          <span class="rd-value">{{ f.value }}</span>
        </div>

        <div v-if="w.matchConditions?.length" class="rd-row">
          <span class="rd-label">{{ $t('sections.webhooks.matchConditions') }}</span>
          <div class="rd-value">
            <div v-for="c in w.matchConditions" :key="c" class="rd-mono">{{ c }}</div>
          </div>
        </div>

        <div class="wh-sub">{{ $t('defs.columns.rules') }}</div>
        <div v-if="!w.rules?.length" class="rd-muted">{{ $t('sections.webhooks.noRules') }}</div>
        <div v-else class="rd-table-wrap">
          <table class="rd-table">
            <thead>
              <tr><th>{{ $t('sections.webhooks.operations') }}</th><th>{{ $t('sections.rules.apiGroups') }}</th><th>{{ $t('sections.webhooks.apiVersions') }}</th><th>{{ $t('defs.tabs.resources') }}</th><th>{{ $t('defs.columns.scope') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="(r, j) in w.rules" :key="j">
                <td>{{ join(r.operations) }}</td>
                <td>{{ join(r.apiGroups) }}</td>
                <td>{{ join(r.apiVersions) }}</td>
                <td>{{ join(r.resources) }}</td>
                <td>{{ r.scope || '*' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue'
import { t } from '../../../i18n'

const props = defineProps<{ resource: any }>()
const emit = defineEmits<{ (e: 'open', p: { id: string; ns: string; name: string }): void }>()

const webhooks = computed<any[]>(() => (Array.isArray(props.resource?.webhooks) ? props.resource.webhooks : []))

// Первый webhook раскрыт сразу, остальные свёрнуты
const opened = ref<Record<number, boolean>>({ 0: true })
const isOpen = (i: number) => !!opened.value[i]
const toggle = (i: number) => { opened.value = { ...opened.value, [i]: !opened.value[i] } }

const join = (v: unknown) => {
  if (!Array.isArray(v) || !v.length) return '—'
  return v.map(x => (x === '' ? '(core)' : x)).join(', ')
}

function fields(w: any) {
  const rows: { label: string; value: string | number }[] = [
    { label: t('sections.webhooks.failurePolicy'), value: w.failurePolicy || '—' },
    { label: t('sections.webhooks.matchPolicy'), value: w.matchPolicy || '—' },
    { label: t('sections.webhooks.sideEffects'), value: w.sideEffects || '—' },
    { label: t('sections.webhooks.timeout'), value: w.timeoutSeconds ? `${w.timeoutSeconds}s` : '—' },
  ]
  if (w.reinvocationPolicy) rows.push({ label: t('sections.webhooks.reinvocationPolicy'), value: w.reinvocationPolicy })
  rows.push(
    { label: t('sections.webhooks.reviewVersions'), value: join(w.admissionReviewVersions) },
    { label: t('sections.webhooks.namespaceSelector'), value: w.namespaceSelector || '—' },
    { label: t('sections.webhooks.objectSelector'), value: w.objectSelector || '—' },
    { label: t('sections.webhooks.caBundle'), value: w.hasCaBundle ? t('sections.webhooks.set') : t('sections.webhooks.notSet') },
  )
  return rows
}

function openService(w: any) {
  emit('open', { id: 'service', ns: w.serviceNamespace, name: w.serviceName })
}
</script>

<style scoped>
.wh-title { font-weight: 700; color: var(--text-strong); padding: 12px 0 8px; text-align: left; }
.wh-name { font-family: 'Courier New', monospace; font-size: 0.85rem; color: #e5e7eb; flex: 1; word-break: break-all; }
.rd-block-head { display: flex; align-items: center; gap: 8px; cursor: pointer; }
.wh-body { display: flex; flex-direction: column; }
.wh-sub { font-weight: 600; color: var(--text-code); padding: 12px 0 8px; }
</style>
