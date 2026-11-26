<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="actions" data-testid="readiness-actions">
    <div v-for="phase in PHASES" :key="phase" class="phase">
      <h3>{{ $t(`readiness.actions.${phase}`) }}</h3>
      <ol>
        <li v-for="a in byPhase(phase)" :key="a.message.code">
          {{ messageText(a.message) }}
          <a v-if="safeUrl(a.url)" href="#" @click.prevent="openUrl(safeUrl(a.url))">{{ $t('readiness.actions.open') }}</a>
        </li>
      </ol>
    </div>
    <p class="muted">{{ $t('readiness.actions.note') }}</p>
  </section>
</template>

<script lang="ts" setup>
import { openUrl } from '../../platform/files'
import { messageText, safeUrl } from './readinessView'
import type { Action } from '../../types/readiness'

const props = defineProps<{ actions: Action[] }>()
const PHASES = ['before', 'after'] as const
const byPhase = (p: 'before' | 'after') => props.actions.filter(a => a.phase === p).sort((a, b) => a.priority - b.priority)
</script>

<style scoped>
.phase h3 { margin: 12px 0 6px; font-size: 15px; }
ol { margin: 0; padding-left: 20px; font-size: 13px; line-height: 1.6; }
a { color: #89b4fa; margin-left: 6px; }
.muted { color: #6c7086; font-size: 12px; }
</style>
