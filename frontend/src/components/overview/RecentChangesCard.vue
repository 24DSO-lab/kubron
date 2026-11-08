<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="ov-card rc">
    <header class="head">
      <h2 class="ov-card-title">{{ $t('overview.changes.title') }}</h2>
      <div class="tabs" role="tablist">
        <button v-for="w in WINDOWS" :key="w.min" type="button" role="tab" :aria-selected="windowMinutes === w.min"
          :class="{ on: windowMinutes === w.min }" @click="emit('update:windowMinutes', w.min)">{{ $t(`overview.changes.window${w.min}`) }}</button>
      </div>
    </header>

    <ul v-if="changes && changes.length" class="list">
      <li v-for="(c, i) in changes" :key="`${c.kind}/${c.namespace}/${c.name}/${c.time}/${i}`">
        <button type="button" class="row" :disabled="!c.target" @click="emit('open', c)">
          <span class="kind" :class="kindClass(c.kind)">{{ c.kind }}</span>
          <span class="name" :title="c.name">{{ c.name }}</span>
          <span class="when">{{ timeAgo(c.time, now) }}</span>
          <span class="sub">{{ c.namespace }}<template v-if="c.detail"> · {{ c.detail }}</template></span>
        </button>
      </li>
    </ul>
    <p v-else-if="changes" class="none">{{ $t('overview.changes.empty') }}</p>
    <p v-else class="none">{{ $t('common.loading') }}</p>
  </section>
</template>

<script lang="ts" setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { timeAgo } from './format'
import type { ChangeEvent } from '../../types/insights'

defineProps<{ changes: ChangeEvent[] | null; windowMinutes: number }>()
const emit = defineEmits<{ (e: 'open', c: ChangeEvent): void; (e: 'update:windowMinutes', v: number): void }>()

const WINDOWS = [{ min: 60 }, { min: 360 }, { min: 1440 }]

const now = ref(Date.now())
let tick: ReturnType<typeof setInterval> | null = null
onMounted(() => { tick = setInterval(() => (now.value = Date.now()), 30_000) })
onUnmounted(() => { if (tick) clearInterval(tick) })

const KIND_CLASS: Record<string, string> = { Rollout: 'k-roll', Helm: 'k-helm', Flux: 'k-flux' }
const kindClass = (k: string) => KIND_CLASS[k] ?? 'k-argo'
</script>

<style scoped>
.head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.head .ov-card-title { margin: 0; }
.tabs { display: flex; gap: 4px; }
.tabs button { padding: 2px 10px; background: transparent; border: 1px solid var(--ov-border); border-radius: 999px; font: inherit; font-size: 11px; color: var(--ov-muted); cursor: pointer; }
.tabs button.on { color: var(--ov-text); border-color: var(--ov-accent); }
.list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; max-height: 300px; overflow: auto; }
.row { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; grid-template-areas: 'kind name when' 'sub sub sub'; gap: 2px 8px; align-items: center; width: 100%; padding: 6px 8px; background: transparent; border: 0; border-radius: 8px; font: inherit; text-align: left; color: var(--ov-text); cursor: pointer; }
.row:disabled { cursor: default; }
.row:not(:disabled):hover, .row:not(:disabled):focus-visible { background: var(--ov-card-hover); outline: none; }
.kind { grid-area: kind; padding: 1px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
.k-roll { color: #60a5fa; background: rgba(96, 165, 250, 0.12); }
.k-helm { color: #a78bfa; background: rgba(167, 139, 250, 0.12); }
.k-argo { color: #f59e0b; background: rgba(245, 158, 11, 0.12); }
.k-flux { color: #5b8def; background: rgba(91, 141, 239, 0.14); }
.name { grid-area: name; font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.when { grid-area: when; font-size: 11px; color: var(--ov-muted); }
.sub { grid-area: sub; font-size: 11px; color: var(--ov-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.none { margin: 0; font-size: 13px; color: var(--ov-muted); }
</style>
