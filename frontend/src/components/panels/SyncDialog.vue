<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="sd-overlay" @click.self="close">
    <div class="sd" role="dialog" aria-modal="true">
      <div class="sd-head">
        <h3 v-if="!bulk">{{ $t('argo.sync.title') }} <span class="sd-name">{{ apps[0].namespace }}/{{ apps[0].name }}</span></h3>
        <h3 v-else>{{ $t('argo.sync.titleBulk') }} <span class="sd-muted">({{ $t('argo.sync.selected', { n: selected.size }) }})</span></h3>
      </div>

      <div class="sd-body">
        <!-- Массовый режим: выбор приложений, OutOfSync предвыбраны -->
        <template v-if="bulk">
          <input v-model="filter" class="sd-input" type="search" :placeholder="$t('argo.sync.filterPlaceholder')" />
          <div class="sd-list">
            <label v-for="a in visibleApps" :key="keyOf(a)" class="sd-app" :class="{ off: a.operationOn }">
              <input type="checkbox" :checked="selected.has(keyOf(a))" :disabled="a.operationOn" @change="toggle(a)" />
              <span class="sd-app-name">{{ a.name }}</span>
              <span class="sd-muted">{{ a.namespace }}</span>
              <span class="sd-tag" :class="a.syncStatus === 'OutOfSync' ? 'warn' : 'ok'">{{ a.operationOn ? $t('argo.sync.syncing') : a.syncStatus }}</span>
            </label>
          </div>
        </template>

        <label v-else class="sd-field">
          <span>{{ $t('defs.columns.revision') }}</span>
          <input v-model="revision" class="sd-input" :placeholder="$t('argo.sync.revisionPlaceholder')" />
        </label>

        <label class="sd-check"><input v-model="prune" type="checkbox" /> {{ $t('argo.sync.prune') }}</label>
        <label class="sd-check"><input v-model="force" type="checkbox" /> {{ $t('argo.sync.force') }}</label>
        <label class="sd-check"><input v-model="dryRun" type="checkbox" /> {{ $t('argo.sync.dryRunOption') }}</label>

        <div v-if="prune && !dryRun" class="sd-risk" :class="{ danger: pruneTargets.length }">
          <template v-if="pruneTargets.length">
            <strong>{{ $t('argo.sync.willDelete', pruneTargets.length) }}</strong>
            <ul>
              <li v-for="target in pruneTargets.slice(0, 8)" :key="target">{{ target }}</li>
            </ul>
            <span v-if="pruneTargets.length > 8">{{ $t('argo.sync.andMore', { n: pruneTargets.length - 8 }) }}</span>
          </template>
          <template v-else>{{ $t('argo.sync.nothingToPrune') }}</template>
        </div>
        <div v-if="force && !dryRun" class="sd-risk danger">{{ $t('argo.sync.forceRisk') }}</div>
      </div>

      <div class="sd-foot">
        <button class="sd-btn" :disabled="busy" @click="close">{{ $t('common.cancel') }}</button>
        <button v-if="bulk" class="sd-btn" :disabled="busy || !selected.size" @click="refreshSelected">{{ $t('common.refresh') }}</button>
        <button class="sd-btn primary" :disabled="busy || !selected.size" @click="submit">
          {{ busy ? $t('argo.sync.starting') : dryRun ? $t('argo.sync.dryRun') : bulk ? $t('argo.sync.submitBulk', { n: selected.size }) : $t('argo.sync.submit') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue'
import { refreshArgoApps, runArgoSync } from '../../resources/argoActions'
import type { ArgoApplicationInfo } from '../../types/argocd'

const props = defineProps<{ apps: ArgoApplicationInfo[] }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const bulk = computed(() => props.apps.length > 1)
const keyOf = (a: ArgoApplicationInfo) => `${a.namespace}/${a.name}`

// OutOfSync первыми
const sorted = computed(() =>
  [...props.apps].sort(
    (a, b) =>
      Number(b.syncStatus === 'OutOfSync') - Number(a.syncStatus === 'OutOfSync') ||
      a.name.localeCompare(b.name),
  ),
)

const filter = ref('')
const visibleApps = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return q ? sorted.value.filter(a => `${a.name} ${a.namespace} ${a.project}`.toLowerCase().includes(q)) : sorted.value
})

// Предвыбор: одно приложение — оно само; массово — OutOfSync без идущей операции
const selected = ref(
  new Set(
    props.apps
      .filter(a => (bulk.value ? a.syncStatus === 'OutOfSync' && !a.operationOn : true))
      .map(keyOf),
  ),
)
function toggle(a: ArgoApplicationInfo) {
  const s = new Set(selected.value)
  if (!s.delete(keyOf(a))) s.add(keyOf(a))
  selected.value = s
}
const selectedApps = computed(() => props.apps.filter(a => selected.value.has(keyOf(a))))

const prune = ref(false)
const force = ref(false)
const dryRun = ref(false)
const revision = ref('')
const busy = ref(false)

/** Ресурсы, которые Argo пометил requiresPruning: именно их удалит sync с prune */
const pruneTargets = computed(() =>
  selectedApps.value.flatMap(a =>
    (a.resources ?? []).filter(r => r.requiresPruning && !r.hook).map(r => `${r.kind}/${r.name} (${a.name})`),
  ),
)

const close = () => { if (!busy.value) emit('close') }

async function submit() {
  busy.value = true
  await runArgoSync(selectedApps.value, {
    revision: bulk.value ? '' : revision.value.trim(),
    prune: prune.value,
    force: force.value,
    dryRun: dryRun.value,
  })
  busy.value = false
  emit('close')
}

async function refreshSelected() {
  busy.value = true
  await refreshArgoApps(selectedApps.value, false)
  busy.value = false
  emit('close')
}
</script>

<style scoped>
.sd-overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 2000; }
.sd { width: 90%; max-width: 560px; max-height: 90vh; display: flex; flex-direction: column; background: var(--panel-raised); border: 1px solid var(--line-strong); border-radius: 8px; box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5); text-align: left; }
.sd-head { padding: 16px; background: var(--panel-deep); border-bottom: 1px solid var(--line-strong); border-radius: 8px 8px 0 0; }
.sd-head h3 { margin: 0; font-size: 1rem; color: #fff; }
.sd-name { color: var(--blue); font-family: 'JetBrains Mono', 'Fira Code', monospace; }
.sd-muted { color: var(--text-gray); font-size: 0.8rem; font-weight: 400; }
.sd-body { padding: 16px 20px; display: flex; flex-direction: column; gap: 12px; overflow-y: auto; }
.sd-input { width: 100%; box-sizing: border-box; padding: 7px 10px; background: var(--panel); color: var(--text-code); border: 1px solid var(--line-strong); border-radius: 4px; font: inherit; font-size: 0.85rem; }
.sd-input:focus { outline: none; border-color: var(--blue-strong); }
.sd-field { display: flex; flex-direction: column; gap: 6px; font-size: 0.85rem; color: var(--text-soft); }
.sd-list { max-height: 240px; overflow-y: auto; border: 1px solid var(--line); border-radius: 4px; }
.sd-app { display: grid; grid-template-columns: auto 1fr auto auto; gap: 10px; align-items: center; padding: 6px 10px; font-size: 0.85rem; color: var(--text-code); cursor: pointer; border-bottom: 1px solid rgba(255, 255, 255, 0.05); }
.sd-app:hover { background: rgba(255, 255, 255, 0.03); }
.sd-app.off { opacity: 0.5; cursor: not-allowed; }
.sd-app-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sd-tag { padding: 1px 8px; border-radius: 999px; font-size: 0.7rem; font-weight: 600; }
.sd-tag.ok { color: var(--green); background: rgba(74, 222, 128, 0.12); }
.sd-tag.warn { color: var(--yellow); background: rgba(250, 204, 21, 0.12); }
.sd-check { display: flex; align-items: center; gap: 8px; font-size: 0.85rem; color: var(--text-code); cursor: pointer; }
.sd-risk { padding: 10px 12px; border-radius: 4px; font-size: 0.8rem; color: var(--text-soft); background: rgba(255, 255, 255, 0.03); border: 1px solid var(--line); }
.sd-risk.danger { color: #fca5a5; background: rgba(239, 68, 68, 0.1); border-color: rgba(239, 68, 68, 0.4); }
.sd-risk ul { margin: 6px 0; padding-left: 18px; font-family: 'Courier New', monospace; }
.sd-foot { display: flex; justify-content: flex-end; gap: 10px; padding: 12px 16px; background: var(--panel-deep); border-top: 1px solid var(--line-strong); border-radius: 0 0 8px 8px; }
.sd-btn { padding: 8px 16px; border-radius: 4px; border: 1px solid var(--line-strong); background: transparent; color: var(--text-code); font-size: 0.9rem; font-weight: 600; cursor: pointer; }
.sd-btn:hover:not(:disabled) { background: rgba(255, 255, 255, 0.06); }
.sd-btn.primary { background: var(--blue-strong); border-color: var(--blue-strong); color: #fff; }
.sd-btn.primary:hover:not(:disabled) { background: var(--blue-deep); }
.sd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>