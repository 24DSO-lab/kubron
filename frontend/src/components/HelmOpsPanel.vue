<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="helmOps.length" class="ho-panel" role="status" aria-live="polite">
    <div v-for="o in helmOps" :key="o.opId" class="ho-op" :class="{ err: !!o.error, ok: o.done && !o.error }">
      <div class="ho-head">
        <span class="ho-icon" aria-hidden="true">{{ o.error ? '✕' : o.done ? '✓' : '⟳' }}</span>
        <span class="ho-title" :title="`${o.namespace}/${o.release}`">{{ helmOpLabel(o.op) }} {{ o.release }}</span>
        <span class="ho-time">{{ elapsed(o) }}</span>
        <button v-if="!o.done && o.cancelable" type="button" class="ho-btn" @click="cancelHelmOp(o.opId)">{{ $t('helm.ops_panel.cancel') }}</button>
        <button v-if="o.done" type="button" class="ho-btn" :aria-label="$t('helm.ops_panel.close')" @click="dismissHelmOp(o.opId)">✕</button>
      </div>
      <div class="ho-phase">{{ o.error ? $t('helm.ops_panel.error') : o.done ? $t('helm.ops_panel.done') : o.phase }}<span class="ho-ns"> · {{ o.namespace }}</span></div>
      <pre v-if="o.error" class="ho-log ho-error">{{ o.error }}</pre>
      <template v-else-if="o.lines.length">
        <pre class="ho-log">{{ (expanded[o.opId] ? o.lines.slice(-30) : o.lines.slice(-3)).join('\n') }}</pre>
        <button type="button" class="ho-more" @click="expanded[o.opId] = !expanded[o.opId]">
          {{ expanded[o.opId] ? $t('helm.ops_panel.hideLog') : $t('helm.ops_panel.showLog') }}
        </button>
      </template>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { cancelHelmOp, dismissHelmOp, helmOpLabel, helmOps, startHelmOps, type HelmOp } from '../composables/useHelmOps'

const { t } = useI18n()

const expanded = reactive<Record<string, boolean>>({})
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined

function elapsed(o: HelmOp) {
  const ms = o.done && o.elapsedMs ? o.elapsedMs : now.value - o.startedAt
  const s = Math.max(0, Math.floor(ms / 1000))
  return s >= 60 ? `${Math.floor(s / 60)}${t('helm.ops_panel.min')} ${String(s % 60).padStart(2, '0')}${t('helm.ops_panel.sec')}` : `${s}${t('helm.ops_panel.sec')}`
}

onMounted(() => {
  startHelmOps()
  timer = setInterval(() => { now.value = Date.now() }, 1000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<style scoped>
.ho-panel { position: fixed; right: 16px; bottom: 16px; z-index: 900; width: 360px; max-height: 70vh; overflow: auto; display: flex; flex-direction: column; gap: 8px; text-align: left; }
.ho-op { background: var(--bg); border: 1px solid var(--surface-0); border-left: 3px solid var(--accent); border-radius: 8px; padding: 10px 12px; box-shadow: 0 6px 24px rgba(0, 0, 0, 0.4); }
.ho-op.ok { border-left-color: var(--ok); }
.ho-op.err { border-left-color: var(--danger); }
.ho-head { display: flex; align-items: center; gap: 8px; }
.ho-icon { width: 16px; text-align: center; color: var(--accent); }
.ho-op.ok .ho-icon { color: var(--ok); }
.ho-op.err .ho-icon { color: var(--danger); }
.ho-title { flex: 1; font-size: 13px; font-weight: 600; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ho-time { font-size: 12px; color: var(--text-muted); font-variant-numeric: tabular-nums; }
.ho-btn { background: transparent; border: 1px solid var(--surface-1); color: var(--text); border-radius: 4px; padding: 2px 8px; font: inherit; font-size: 12px; cursor: pointer; }
.ho-btn:hover { border-color: var(--accent); }
.ho-phase { margin-top: 4px; font-size: 12px; color: var(--text-muted); }
.ho-ns { color: var(--text-subtle); }
.ho-log { margin: 6px 0 0; padding: 6px 8px; background: #12141f; border-radius: 4px; font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 11px; line-height: 1.4; color: var(--gray-400); white-space: pre-wrap; word-break: break-word; max-height: 220px; overflow: auto; }
.ho-error { color: var(--red-soft); }
.ho-more { margin-top: 4px; background: none; border: 0; color: var(--accent); font: inherit; font-size: 12px; cursor: pointer; padding: 0; }
</style>
