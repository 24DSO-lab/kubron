<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section v-if="releases.length" class="ov-card hc">
    <h2 class="ov-card-title">Helm</h2>

    <div class="hc-stats">
      <button type="button" class="hc-stat" @click="emit('navigate', 'helm-releases')">
        <span class="hc-n">{{ releases.length }}</span><span class="hc-l">{{ $t('helm.helm_card.releases') }}</span>
      </button>
      <button type="button" class="hc-stat" :class="{ d: failed > 0 }" @click="emit('navigate', 'helm-releases')">
        <span class="hc-n">{{ failed }}</span><span class="hc-l">{{ $t('helm.helm_card.failed') }}</span>
      </button>
      <button type="button" class="hc-stat" :class="{ w: pending > 0 }" @click="emit('navigate', 'helm-releases')">
        <span class="hc-n">{{ pending }}</span><span class="hc-l">{{ $t('helm.helm_card.pending') }}</span>
      </button>
      <button type="button" class="hc-stat" :class="{ w: updates.length > 0 }" @click="emit('navigate', 'helm-releases')">
        <span class="hc-n">{{ updates.length }}</span><span class="hc-l">{{ $t('helm.helm_card.updates') }}</span>
      </button>
    </div>

    <ul v-if="updates.length" class="hc-list">
      <li v-for="r in updates.slice(0, 5)" :key="`${r.namespace}/${r.name}`" class="hc-row">
        <span class="hc-name" :title="`${r.namespace}/${r.name}`">{{ r.namespace }}/{{ r.name }}</span>
        <span class="hc-ver">{{ r.chartVersion }} → <strong>{{ r.latestVersion }}</strong></span>
      </li>
      <li v-if="updates.length > 5" class="hc-more">{{ $t('helm.helm_card.more', { count: updates.length - 5 }) }}</li>
    </ul>
    <p v-else class="hc-none">{{ $t('helm.helm_card.allCurrent') }}</p>
  </section>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'
import { callApp } from '../../resources/wails'
import { helmLiveTick, startHelmLive } from '../../composables/useHelmLive'

interface Rel { name: string; namespace: string; chartVersion: string; status: string; latestVersion?: string }

const emit = defineEmits<{ (e: 'navigate', view: string): void }>()
const releases = ref<Rel[]>([])

const failed = computed(() => releases.value.filter(r => r.status === 'failed').length)
const pending = computed(() => releases.value.filter(r => r.status.startsWith('pending')).length)
const updates = computed(() => releases.value.filter(r => !!r.latestVersion))

let seq = 0
async function load() {
  const my = ++seq
  try {
    const r = ((await callApp('GetHelmReleases')) as Rel[]) ?? []
    if (my === seq) releases.value = r
  } catch {
    // нет прав или Helm не используется: карточка просто скрыта
    if (my === seq) releases.value = []
  }
}

onMounted(() => { startHelmLive(); load() })
watch(helmLiveTick, load)
</script>

<style scoped>
.hc-stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin-bottom: 10px; }
.hc-stat { display: flex; flex-direction: column; align-items: flex-start; gap: 2px; padding: 8px 10px; border: 1px solid var(--surface-0); border-radius: 6px; background: transparent; color: inherit; font: inherit; cursor: pointer; text-align: left; }
.hc-stat:hover { border-color: var(--surface-1); }
.hc-n { font-size: 20px; font-weight: 600; color: var(--text); }
.hc-l { font-size: 12px; color: var(--text-muted); }
.hc-stat.w .hc-n { color: var(--warn); }
.hc-stat.d .hc-n { color: var(--danger); }
.hc-list { list-style: none; margin: 0; padding: 0; }
.hc-row { display: flex; justify-content: space-between; gap: 12px; padding: 5px 0; border-top: 1px solid #232334; font-size: 13px; }
.hc-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hc-ver { color: var(--text-muted); white-space: nowrap; font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12px; }
.hc-ver strong { color: var(--warn); }
.hc-more, .hc-none { margin: 6px 0 0; font-size: 12px; color: var(--text-subtle); }
</style>
