<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="diff" data-testid="readiness-diff">
    <h3>{{ $t('readiness.diff.title') }}</h3>
    <p v-if="unavailable" class="muted">{{ $t('readiness.diff.first') }}</p>
    <template v-else-if="diff">
      <p class="muted">{{ $t('readiness.diff.against', { id: diff.previousId }) }}</p>
      <p v-if="diff.targetChanged" class="warn">{{ $t('readiness.diff.targetChanged', { before: diff.targetBefore || $t('readiness.noTarget'), after: diff.targetAfter || $t('readiness.noTarget') }) }}</p>
      <p v-if="diff.matrixChanged" class="warn">{{ $t('readiness.diff.matrixChanged', { before: diff.matrixBefore, after: diff.matrixAfter }) }}</p>
      <p v-if="diff.catalogChanged" class="warn">{{ $t('readiness.diff.catalogChanged', { before: diff.catalogBefore, after: diff.catalogAfter }) }}</p>
      <p v-if="!diffHasChanges(diff)" class="muted">{{ $t('readiness.diff.same', { n: diff.unchanged }) }}</p>
      <template v-else>
        <div v-if="diff.new.length"><h4>{{ $t('readiness.diff.new', { n: diff.new.length }) }}</h4>
          <ul><li v-for="f in diff.new" :key="f.key"><span :class="['sev', f.severity]">{{ $t(`readiness.severity.${f.severity}`) }}</span> {{ findingTitle(f) }}</li></ul></div>
        <div v-if="diff.resolved.length"><h4>{{ $t('readiness.diff.resolved', { n: diff.resolved.length }) }}</h4>
          <ul><li v-for="f in diff.resolved" :key="f.key">{{ findingTitle(f) }}</li></ul></div>
        <div v-if="diff.changed.length"><h4>{{ $t('readiness.diff.changed', { n: diff.changed.length }) }}</h4>
          <ul><li v-for="c in diff.changed" :key="c.key">
            {{ findingTitle(c.after) }}:
            <template v-if="c.severity">{{ $t(`readiness.severity.${c.before.severity}`) }} → {{ $t(`readiness.severity.${c.after.severity}`) }}; </template>
            <template v-if="c.status">{{ $t(`readiness.status.${c.before.status}`) }} → {{ $t(`readiness.status.${c.after.status}`) }}; </template>
            <template v-if="c.version">{{ c.before.component?.version || '—' }} → {{ c.after.component?.version || '—' }}</template>
          </li></ul></div>
        <p class="muted">{{ $t('readiness.diff.unchanged', { n: diff.unchanged }) }}</p>
      </template>
    </template>
  </section>
</template>

<script lang="ts" setup>
import { diffHasChanges, findingTitle } from './readinessView'
import type { Diff } from '../../types/readiness'

defineProps<{ diff: Diff | null; unavailable: boolean }>()
</script>

<style scoped>
.diff { border: 1px solid #313244; border-radius: 10px; padding: 14px 16px; margin: 0 0 20px; }
h3 { margin: 0 0 6px; font-size: 15px; } h4 { margin: 10px 0 4px; font-size: 13px; color: #a6adc8; }
ul { margin: 0; padding-left: 18px; font-size: 13px; line-height: 1.6; }
.muted { color: #6c7086; font-size: 12px; margin: 4px 0; }
.warn { color: #f9e2af; font-size: 12px; margin: 4px 0; }
.sev { font-size: 11px; font-weight: 600; padding: 1px 6px; border-radius: 4px; background: #313244; }
.sev.blocker { color: #f38ba8; } .sev.warning { color: #f9e2af; } .sev.info { color: #89b4fa; }
</style>
