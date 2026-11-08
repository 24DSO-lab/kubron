<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ov" data-testid="flux-dashboard">
    <header class="ov-header">
      <h1>Flux CD</h1>
      <div class="ov-header-right">
        <span v-if="objects && error" class="ov-stale" :title="error">{{ $t('fluxDash.updateFailed') }}</span>
        <span v-else-if="updatedAt" class="ov-updated">{{ $t('fluxDash.updated', { time: formatTime(updatedAt) }) }}</span>
        <button type="button" class="ov-btn" :disabled="refreshing" data-testid="flux-dash-refresh" @click="refresh">
          {{ refreshing ? $t('fluxDash.refreshing') : $t('common.refresh') }}
        </button>
      </div>
    </header>

    <div v-if="unsupported" class="ov-state" role="alert" data-testid="flux-dash-unsupported">
      <p>{{ $t('fluxDash.unsupported.title') }}</p>
      <p class="ov-state-detail">{{ $t('fluxDash.unsupported.hint') }}</p>
    </div>

    <p v-else-if="loading && !objects" class="ov-state" role="status" data-testid="flux-dash-loading">{{ $t('fluxDash.loading') }}</p>

    <div v-else-if="!objects" class="ov-state ov-state-error" role="alert" data-testid="flux-dash-error">
      <p>{{ $t('fluxDash.loadFailed') }}</p>
      <p class="ov-state-detail">{{ error }}</p>
      <button type="button" class="ov-btn ov-btn-primary" @click="refresh">{{ $t('common.retry') }}</button>
    </div>

    <p v-else-if="!summary.total" class="ov-state" data-testid="flux-dash-empty">{{ $t('fluxDash.empty') }}</p>

    <template v-else>
      <div class="ov-stats" data-testid="flux-dash-stats">
        <StatCard v-for="s in stats" :key="s.id" :title="s.title" :value="s.value" :sub="s.sub" :tone="s.tone"
          :data-testid="`flux-dash-stat-${s.id}`" @select="emit('navigate', 'flux-kustomizations')" />
      </div>

      <div class="ov-row ov-row-2">
        <section class="ov-card" data-testid="flux-dash-problems">
          <h2 class="ov-card-title">{{ $t('fluxDash.needsAttention') }}</h2>
          <p v-if="!summary.problems.length" class="flux-empty">{{ $t('fluxDash.allGood') }}</p>
          <ul v-else class="flux-list">
            <li v-for="o in summary.problems.slice(0, 10)" :key="key(o)">
              <button type="button" class="flux-row" data-testid="flux-dash-problem" @click="open(o)">
                <span class="flux-name">{{ o.name }}</span>
                <span class="flux-sub">{{ o.namespace }} · {{ $t(`fluxDash.kind.${o.kind}`) }}<template v-if="o.message"> · {{ o.message }}</template></span>
                <span class="flux-tags"><span class="flux-tag" :class="tone(o.state)">{{ stateLabel(o.state) }}</span></span>
              </button>
            </li>
          </ul>
          <button v-if="summary.problems.length > 10" type="button" class="flux-more" @click="emit('navigate', 'flux-kustomizations')">
            {{ $t('fluxDash.viewAll', { n: summary.problems.length }) }}
          </button>
        </section>

        <section class="ov-card" data-testid="flux-dash-recent">
          <h2 class="ov-card-title">{{ $t('fluxDash.recentReconciles') }}</h2>
          <p v-if="!summary.recent.length" class="flux-empty">{{ $t('fluxDash.noReconciles') }}</p>
          <ul v-else class="flux-list">
            <li v-for="o in summary.recent" :key="key(o)">
              <button type="button" class="flux-row" data-testid="flux-dash-recent-row" @click="open(o)">
                <span class="flux-name">{{ o.name }}</span>
                <span class="flux-sub">{{ formatWhen(o.lastReconcileAt) }} · {{ shortRevision(o.revision) || '—' }}</span>
                <span class="flux-tags"><span class="flux-tag" :class="tone(o.state)">{{ stateLabel(o.state) }}</span></span>
              </button>
            </li>
          </ul>
        </section>
      </div>

      <div class="ov-row ov-row-2">
        <GroupCard :title="$t('fluxDash.byNamespace')" :rows="summary.byNamespace" />
        <GroupCard :title="$t('fluxDash.bySource')" :rows="sourceRows" />
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import { computed, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime, formatTime } from '../../i18n/format'
import StatCard from '../overview/StatCard.vue'
import GroupCard from '../overview/ArgoGroupCard.vue'
import { useFlux } from '../../composables/useFlux'
import { useFluxObjects } from '../../composables/useFluxObjects'
import { shortRevision, summarizeFlux } from '../../resources/flux'
import { resourceActions } from '../../resources/actions'
import type { FluxHelmReleaseInfo, FluxKustomizationInfo, FluxState } from '../../types/flux'
import '../overview/overview.css'

type FluxObject = FluxKustomizationInfo | FluxHelmReleaseInfo

const { t } = useI18n()
const props = withDefaults(defineProps<{ clusterName?: string }>(), { clusterName: '' })
const emit = defineEmits<{ (e: 'navigate', view: string): void }>()

const { fluxStatus } = useFlux()
const { objects, loading, refreshing, error, updatedAt, refresh } = useFluxObjects(toRef(props, 'clusterName'))

/** Flux есть, но устарел: объекты не разбираются, вместо данных — подсказка (ADR-0023, раздел 3) */
const unsupported = computed(() => fluxStatus.value.state === 'unsupportedVersion')
const summary = computed(() => summarizeFlux<FluxObject>(objects.value ?? []))

/** Источник без ссылки подписывается словом, а не пустой строкой */
const sourceRows = computed(() => summary.value.bySource.map(r => ({ ...r, name: r.name || t('fluxDash.noSource') })))

type Tone = 'ok' | 'warn' | 'danger'

const stats = computed(() => {
  const s = summary.value
  const ofTotal = t('fluxDash.stats.ofTotal', { n: s.total })
  return [
    { id: 'ready', title: t('fluxDash.stats.ready'), value: s.ready, sub: ofTotal, tone: (s.ready === s.total ? 'ok' : 'warn') as Tone },
    { id: 'reconciling', title: t('fluxDash.stats.reconciling'), value: s.reconciling, sub: s.reconciling ? t('fluxDash.stats.inProgress') : t('fluxDash.stats.none'), tone: (s.reconciling ? 'warn' : 'ok') as Tone },
    { id: 'failed', title: t('fluxDash.stats.failed'), value: s.failed, sub: s.failed ? t('fluxDash.stats.needsAction') : t('fluxDash.stats.none'), tone: (s.failed ? 'danger' : 'ok') as Tone },
    { id: 'stalled', title: t('fluxDash.stats.stalled'), value: s.stalled, sub: s.stalled ? t('fluxDash.stats.needsAction') : t('fluxDash.stats.none'), tone: (s.stalled ? 'danger' : 'ok') as Tone },
    // отдельная плитка: приостановленный объект ничего не применяет, но и не сломан
    { id: 'suspended', title: t('fluxDash.stats.suspended'), value: s.suspended, sub: t('fluxDash.stats.suspendedObjects', { n: s.suspended }), tone: (s.suspended ? 'warn' : 'ok') as Tone },
  ]
})

const key = (o: FluxObject) => `${o.kind}/${o.namespace}/${o.name}`

const KNOWN: readonly string[] = ['Ready', 'Reconciling', 'Suspended', 'Failed', 'Stalled']
/** Подпись состояния; значение вне контракта показывается как Unknown */
const stateLabel = (s: FluxState) => t(`flux.state.${KNOWN.includes(s) ? s : 'Unknown'}`)

function tone(state: FluxState): string {
  switch (state) {
    case 'Ready': return 't-ok'
    case 'Reconciling': case 'Suspended': return 't-warn'
    case 'Failed': case 'Stalled': return 't-err'
    default: return 't-unk'
  }
}

function formatWhen(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : formatDateTime(d)
}

function open(o: FluxObject) {
  resourceActions.open(o.kind === 'HelmRelease' ? 'flux-helmrelease' : 'flux-kustomization', o.namespace, o.name, o)
}
</script>

<style scoped>
.ov-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.ov-header h1 { font-size: 20px; font-weight: 600; margin: 0; }
.ov-header-right { display: flex; align-items: center; gap: 12px; }
.ov-updated { font-size: 12px; color: var(--ov-muted); }
.ov-stale { font-size: 12px; color: var(--ov-warn); }
.ov-state { padding: 60px 0; text-align: center; font-size: 14px; color: var(--ov-muted); }
.ov-state-error { color: var(--ov-text); }
.ov-state-error p { margin-bottom: 8px; }
.ov-state-detail { font-size: 12px; color: var(--ov-muted); }
.ov-stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 14px; margin-bottom: 14px; }
.ov-row { display: grid; gap: 14px; margin-bottom: 14px; }
.ov-row-2 { grid-template-columns: repeat(2, minmax(0, 1fr)); }
@container (max-width: 760px) { .ov-row-2 { grid-template-columns: minmax(0, 1fr); } }

.flux-empty { margin: 0; font-size: 13px; color: var(--ov-muted); }
.flux-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
.flux-row {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; grid-template-areas: 'name tags' 'sub tags';
  gap: 2px 12px; align-items: center; width: 100%; padding: 8px 10px; background: transparent; border: 0;
  border-radius: 8px; font: inherit; text-align: left; color: var(--ov-text); cursor: pointer;
}
.flux-row:hover { background: var(--ov-card-hover); }
.flux-row:focus-visible { outline: 2px solid var(--ov-accent); outline-offset: -2px; }
.flux-name { grid-area: name; font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.flux-sub { grid-area: sub; font-size: 12px; color: var(--ov-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.flux-tags { grid-area: tags; display: flex; gap: 6px; flex-wrap: wrap; justify-content: flex-end; }
.flux-tag { padding: 1px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
.t-ok { color: var(--ov-ok); background: rgba(74, 222, 128, 0.12); }
.t-warn { color: var(--ov-warn); background: rgba(251, 191, 36, 0.12); }
.t-err { color: var(--ov-danger); background: rgba(248, 113, 113, 0.12); }
.t-unk { color: var(--ov-muted); background: rgba(139, 145, 173, 0.12); }
.flux-more { margin-top: 8px; padding: 6px 10px; background: transparent; border: 0; font: inherit; font-size: 12px; color: var(--ov-accent); cursor: pointer; align-self: flex-start; }
.flux-more:hover { text-decoration: underline; }
</style>
