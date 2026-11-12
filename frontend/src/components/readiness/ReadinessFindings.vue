<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section :data-testid="`readiness-group-${severity}`" class="group">
    <h3>
      <span :class="['dot', severity]"></span>
      {{ $t(`readiness.severity.${severity}`) }} <span class="count">{{ findings.length }}</span>
    </h3>
    <p v-if="!findings.length" class="none">{{ $t(`readiness.findings.none.${severity}`) }}</p>
    <ul v-else class="list">
      <li v-for="f in findings" :key="f.key" :class="['item', severity]" :data-testid="`readiness-finding`">
        <div class="item-head">
          <span class="prio" :title="$t('readiness.findings.priority')">{{ f.priority }}</span>
          <strong>{{ findingTitle(f) }}</strong>
          <span :class="['status', 'st-' + f.status]">{{ $t(`readiness.status.${f.status}`) }}</span>
          <span class="muted">{{ $t(`readiness.source.${f.source}`) }} · {{ formatDateTime(f.observedAt) }}</span>
        </div>
        <p class="expl">{{ messageText(f.explanation) }}</p>

        <template v-if="f.api">
          <p class="muted">
            <span v-if="f.api.deprecatedIn">{{ $t('readiness.api.deprecatedIn', { v: f.api.deprecatedIn }) }}</span>
            <span v-if="f.api.removedIn"> · {{ $t('readiness.api.removedIn', { v: f.api.removedIn }) }}</span>
            <span v-if="f.api.replacement"> · {{ $t('readiness.api.replacement', { v: f.api.replacement }) }}</span>
          </p>
          <details v-if="f.api.resources?.length" class="res">
            <summary>{{ $t('readiness.api.objects', { n: f.api.resourceCount }) }}</summary>
            <ul>
              <li v-for="r in f.api.resources" :key="resourceLabel(r.namespace, r.name)">
                <code>{{ resourceLabel(r.namespace, r.name) }}</code>
                <span class="muted"> · {{ r.evidence.map(e => $t(`readiness.api.evidence.${e}`)).join(', ') }}</span>
              </li>
              <li v-if="f.api.resourcesTruncated" class="muted">{{ $t('readiness.api.more', { n: f.api.resourceCount - f.api.resources.length }) }}</li>
            </ul>
          </details>
        </template>

        <template v-if="f.component">
          <p class="muted">
            <span v-if="f.component.version">{{ $t('readiness.component.version', { v: f.component.version }) }}</span>
            <span v-if="f.component.state && f.component.state !== 'unknown'"> · {{ $t(`readiness.component.state.${f.component.state}`, { ready: f.component.ready ?? 0, desired: f.component.desired ?? 0 }) }}</span>
            <span v-if="f.component.image"> · <code>{{ f.component.image }}</code></span>
          </p>
          <p v-if="f.component.noteCode || f.component.note" class="note">
            {{ noteText(f.component.noteCode, f.component.note) }}
            <a v-if="safeUrl(f.component.noteUrl)" href="#" @click.prevent="openUrl(safeUrl(f.component.noteUrl))">{{ $t('readiness.component.more') }}</a>
          </p>
          <p v-if="f.component.docsUrl" class="muted">
            {{ $t('readiness.component.source') }}:
            <a v-if="safeUrl(f.component.docsUrl)" href="#" @click.prevent="openUrl(safeUrl(f.component.docsUrl))">{{ $t('readiness.component.docs') }}</a>
            · {{ $t('readiness.component.checkedAt', { date: f.component.checkedAt ?? '' }) }}
          </p>
        </template>

        <p v-if="f.recommendation" class="rec">
          <strong>{{ $t('readiness.findings.recommendation') }}:</strong> {{ messageText(f.recommendation) }}
          <a v-if="f.api?.guideUrl && safeUrl(f.api.guideUrl)" href="#" @click.prevent="openUrl(safeUrl(f.api.guideUrl))">{{ $t('readiness.api.guide') }}</a>
        </p>
      </li>
    </ul>
  </section>
</template>

<script lang="ts" setup>
import { formatDateTime } from '../../i18n/format'
import { openUrl } from '../../platform/files'
import { findingTitle, messageText, noteText, resourceLabel, safeUrl } from './readinessView'
import type { Finding, Severity } from '../../types/readiness'

defineProps<{ severity: Severity; findings: Finding[] }>()
</script>

<style scoped>
.group { margin: 0 0 22px; }
.group h3 { margin: 0 0 8px; font-size: 15px; display: flex; align-items: center; gap: 8px; }
.count { color: #6c7086; font-weight: 400; }
.dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
.dot.blocker { background: #f38ba8; } .dot.warning { background: #f9e2af; } .dot.info { background: #89b4fa; }
.none { color: #6c7086; font-size: 13px; margin: 0; }
.list { list-style: none; margin: 0; padding: 0; display: grid; gap: 10px; }
.item { border: 1px solid #313244; border-left-width: 4px; border-radius: 8px; padding: 10px 12px; }
.item.blocker { border-left-color: #f38ba8; } .item.warning { border-left-color: #f9e2af; } .item.info { border-left-color: #89b4fa; }
.item-head { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.prio { min-width: 22px; text-align: center; border-radius: 4px; background: #313244; color: #a6adc8; font-size: 11px; padding: 1px 4px; }
.status { font-size: 11px; font-weight: 600; padding: 2px 8px; border-radius: 999px; background: #313244; color: #cdd6f4; }
.st-removed { background: rgba(243, 139, 168, 0.18); color: #f38ba8; }
.st-deprecated, .st-attention, .st-known_limitation, .st-insufficient_data { background: rgba(249, 226, 175, 0.15); color: #f9e2af; }
.st-not_checked, .st-partial { background: rgba(250, 179, 135, 0.18); color: #fab387; }
.st-confirmed, .st-ok { background: rgba(166, 227, 161, 0.15); color: #a6e3a1; }
.expl { margin: 6px 0 0; font-size: 13px; line-height: 1.45; }
.muted { color: #6c7086; font-size: 12px; margin: 4px 0 0; }
.note { font-size: 12px; color: #a6adc8; margin: 4px 0 0; }
.rec { font-size: 13px; margin: 8px 0 0; color: #cdd6f4; }
.res { margin-top: 6px; font-size: 12px; } .res summary { cursor: pointer; color: #a6adc8; }
.res ul { margin: 4px 0 0; padding-left: 18px; }
a { color: #89b4fa; margin-left: 6px; }
code { font-size: 12px; word-break: break-all; }
</style>
