<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="br" :aria-label="$t('baseline.result.title')">
    <header class="br-head">
      <h3>{{ $t('baseline.result.title') }}</h3>
      <span class="br-muted">
        {{ $t('baseline.result.checkedAt', { time: formatDateTime(result.checkedAt) }) }}
        <template v-if="result.initiator"> · {{ $t('baseline.result.by', { user: result.initiator }) }}</template>
      </span>
    </header>

    <p v-if="result.rulesVersion !== result.versionRulesVersion" class="br-warn">
      {{ $t('baseline.result.rulesVersions', { current: result.rulesVersion, version: result.versionRulesVersion }) }}
    </p>
    <p v-for="c in result.caveats ?? []" :key="c" class="br-warn">{{ $te(`baseline.caveat.${c}`) ? $t(`baseline.caveat.${c}`) : c }}</p>

    <ul class="br-summary">
      <li>{{ $t('baseline.summary.total', { count: result.summary.total }) }}</li>
      <li class="ok">{{ $t('baseline.summary.matched', { count: result.summary.matched }) }}</li>
      <li class="warn">{{ $t('baseline.summary.changed', { count: result.summary.changed }) }}</li>
      <li class="bad">{{ $t('baseline.summary.missing', { count: result.summary.missing }) }}</li>
      <li class="muted">{{ $t('baseline.summary.unchecked', { count: result.summary.unchecked }) }}</li>
      <li v-if="result.summary.suppressedChanges" class="muted">{{ $t('baseline.summary.suppressed', { count: result.summary.suppressedChanges }) }}</li>
    </ul>
    <p v-if="result.summary.unchecked" class="br-muted">
      {{ $t('baseline.summary.uncheckedNote') }}
      <template v-for="(n, reason) in result.summary.uncheckedByReason ?? {}" :key="reason">
        <br />{{ $t(`baseline.reason.${reason}`) }}: {{ n }}
      </template>
    </p>

    <p v-if="!rows.length" class="br-muted">{{ $t('baseline.result.empty') }}</p>
    <table v-else class="br-table">
      <thead>
        <tr>
          <th>{{ $t('baseline.result.columns.resource') }}</th>
          <th>{{ $t('baseline.result.columns.status') }}</th>
          <th>{{ $t('baseline.result.columns.priority') }}</th>
          <th>{{ $t('baseline.result.columns.details') }}</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="it in rows" :key="it.ref.kind + it.ref.namespace + it.ref.name">
          <tr>
            <td><strong>{{ refLabel(it.ref) }}</strong></td>
            <td><span class="br-badge" :class="statusTone(it.status)">{{ $t(`baseline.status.${it.status}`) }}</span></td>
            <td class="br-muted">{{ it.priority ? $t(`baseline.priority.${it.priority}`) : $t('common.dash') }}</td>
            <td>
              <span v-if="it.status === 'unchecked' && it.reason" class="br-muted">{{ $t(`baseline.reason.${it.reason}`) }}</span>
              <button v-else-if="it.changes?.length" class="br-link" type="button" :aria-expanded="open.has(key(it))" @click="toggle(it)">
                {{ open.has(key(it)) ? $t('baseline.result.hideChanges') : $t('baseline.result.showChanges') }} ({{ it.changes.length }})
              </button>
              <span v-if="it.suppressed?.length" class="br-muted br-sup">{{ $t('baseline.result.suppressed', { ids: it.suppressed.join(', ') }) }}</span>
            </td>
          </tr>
          <tr v-if="open.has(key(it))" class="br-detail">
            <td colspan="4">
              <ul class="br-changes">
                <li v-for="c in it.changes" :key="c.path">
                  <code class="br-path">{{ c.path }}</code>
                  <span class="br-badge" :class="priorityTone(c.priority)">{{ $t(`baseline.priority.${c.priority}`) }}</span>
                  <template v-if="c.sensitive">
                    <span>{{ c.key
                      ? $t('baseline.change.sensitive', { key: c.key, op: $t(`baseline.change.op.${c.op}`) })
                      : $t('baseline.change.sensitiveNoKey', { op: $t(`baseline.change.op.${c.op}`) }) }}</span>
                  </template>
                  <template v-else>
                    <span v-if="c.old !== undefined && c.old !== ''" class="br-val old">{{ $t('baseline.change.old') }}: <code>{{ c.old }}</code></span>
                    <span v-if="c.new !== undefined && c.new !== ''" class="br-val new">{{ $t('baseline.change.new') }}: <code>{{ c.new }}</code></span>
                    <span v-if="!c.old && !c.new" class="br-muted">{{ $t(`baseline.change.op.${c.op}`) }}</span>
                  </template>
                  <span v-if="c.source" class="br-muted br-src">{{ sourceText(c.source) }}</span>
                  <button v-if="canManage && it.status === 'changed'" class="br-link" type="button" @click="emit('add-exception', it.ref, c.path)">
                    {{ $t('baseline.change.addException') }}
                  </button>
                </li>
              </ul>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive } from 'vue'
import { t } from '../../i18n'
import { formatDateTime } from '../../i18n/format'
import { refLabel, sortItems, statusTone } from '../../composables/useBaseline'
import type { BaselineChangeSource, BaselineCheckResult, BaselineItem, BaselinePriority, BaselineRef } from '../../types/baseline'

const props = defineProps<{ result: BaselineCheckResult; canManage: boolean }>()
const emit = defineEmits<{ 'add-exception': [ref: BaselineRef, path: string] }>()

const rows = computed(() => sortItems(props.result.items))
const open = reactive(new Set<string>())

const key = (it: BaselineItem) => `${it.ref.kind}/${it.ref.namespace ?? ''}/${it.ref.name}`
function toggle(it: BaselineItem) { const k = key(it); if (open.has(k)) open.delete(k); else open.add(k) }

function priorityTone(p: BaselinePriority): string { return p === 'high' ? 'bad' : p === 'medium' ? 'warn' : 'muted' }

/** Источник показывается только с доказательством; имя человека бэкенд не отдаёт */
function sourceText(s: BaselineChangeSource): string {
  const time = formatDateTime(s.at)
  if (s.kind === 'kubron-audit') return t('baseline.change.sourceAudit', { time })
  return s.manager
    ? t('baseline.change.sourceManagedFields', { manager: s.manager, time })
    : t('baseline.change.sourceManagedFieldsNoName', { time })
}
</script>

<style scoped>
.br { display: flex; flex-direction: column; gap: 10px; }
.br-head { display: flex; gap: 12px; align-items: baseline; flex-wrap: wrap; }
.br-head h3 { margin: 0; font-size: 15px; }
.br-muted { color: #6c7086; font-size: 12px; }
.br-warn { color: #f9e2af; margin: 0; font-size: 13px; }
.br-summary { display: flex; gap: 14px; flex-wrap: wrap; list-style: none; margin: 0; padding: 0; font-size: 13px; }
.br-summary .ok { color: #a6e3a1; } .br-summary .warn { color: #f9e2af; } .br-summary .bad { color: #f38ba8; } .br-summary .muted { color: #a6adc8; }
.br-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.br-table th { text-align: left; padding: 8px 10px; color: #a6adc8; font-weight: 600; border-bottom: 1px solid #313244; }
.br-table td { padding: 8px 10px; border-bottom: 1px solid #232334; vertical-align: top; }
.br-badge { padding: 1px 8px; border-radius: 10px; font-size: 12px; background: #313244; }
.br-badge.ok { color: #a6e3a1; background: rgba(166, 227, 161, 0.12); }
.br-badge.warn { color: #f9e2af; background: rgba(249, 226, 175, 0.12); }
.br-badge.bad { color: #f38ba8; background: rgba(243, 139, 168, 0.12); }
.br-badge.muted { color: #a6adc8; }
.br-link { background: none; border: 0; color: #89b4fa; cursor: pointer; padding: 0; font-size: 12px; }
.br-sup { display: block; }
.br-detail td { background: #181825; }
.br-changes { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.br-changes li { display: flex; flex-wrap: wrap; gap: 8px; align-items: baseline; }
.br-path { color: #cdd6f4; }
.br-val code { font-family: ui-monospace, Menlo, Consolas, monospace; word-break: break-all; }
.br-val.old code { color: #f38ba8; } .br-val.new code { color: #a6e3a1; }
.br-src { flex-basis: 100%; }
</style>
