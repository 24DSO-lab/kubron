<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <aside class="details" data-testid="map-details" :aria-label="$t('incidentsMap.details.title')" aria-live="polite">
    <p v-if="!node" class="empty" data-testid="map-details-empty">{{ $t('incidentsMap.details.empty') }}</p>
    <template v-else>
      <header>
        <h4 data-testid="map-details-title">{{ node.variant === 'group' ? groupLabel : `${node.kind}/${node.name}` }}</h4>
        <button type="button" class="link" data-testid="map-details-close" @click="emit('clear')">{{ $t('incidentsMap.details.close') }}</button>
      </header>
      <p class="meta">
        {{ $t('incidentsMap.details.namespace') }}: {{ node.namespace || $t('incidentsMap.details.clusterScoped') }}
      </p>
      <p class="state" :class="`h-${node.health}`" data-testid="map-details-state">
        {{ $t('incidentsMap.details.state') }}: <span class="icon" aria-hidden="true">{{ VARIANT_ICONS[node.variant] || HEALTH_ICONS[node.health] }}</span>
        {{ node.variant === 'group' ? $t('incidentsMap.health.Healthy') : $t(`incidentsMap.health.${node.health}`) }}
        <span v-if="node.candidate" class="tag">{{ $t('incidentsMap.candidate') }}</span>
        <span v-if="node.target" class="tag">{{ $t('incidentsMap.target') }}</span>
      </p>
      <!-- Нет ≠ не знаем: у Missing и у Unknown свои пояснения, у Unknown — обязательно с причиной. -->
      <p v-if="node.variant === 'secret'" class="note" data-testid="map-details-secret">{{ $t('incidentsMap.details.secretNote') }}</p>
      <p v-else-if="node.variant === 'placeholder'" class="note" data-testid="map-details-placeholder">{{ $t('incidentsMap.details.placeholderNote') }}</p>
      <p v-else-if="node.variant === 'unknown'" class="note" data-testid="map-details-unknown">{{ $t('incidentsMap.details.unknownNote', { reason: unknownText }) }}</p>
      <p v-if="reasonText" class="note" data-testid="map-details-reason">{{ reasonText }}</p>

      <template v-if="node.variant === 'group'">
        <p class="note">{{ $t('incidentsMap.details.members', { count: node.members?.length ?? 0 }) }}</p>
        <button type="button" class="btn" data-testid="map-expand" @click="emit('expand', node.key)">{{ $t('incidentsMap.group.expand', { kind: node.kind }) }}</button>
      </template>
      <template v-else>
        <section v-if="relations.length" data-testid="map-relations">
          <h5>{{ $t('incidentsMap.details.relations') }}</h5>
          <ul>
            <li v-for="(r, i) in relations" :key="i" :data-state="r.state">
              {{ $t(`incidentsMap.edge.${r.type}`) }} · {{ $t(`incidentsMap.direction.${r.direction}`) }} {{ r.other.kind }}/{{ r.other.name }}
              <span class="faint">({{ $t(`incidentsMap.edgeState.${r.state}`) }})</span>
              <!-- Причина связи (например, необязательная ссылка на отсутствующий объект) — пояснение, а не ошибка: состояние связи не меняется -->
              <span v-if="reasonLabel(r.reason)" class="faint" data-testid="map-relation-reason"> — {{ reasonLabel(r.reason) }}</span>
            </li>
          </ul>
        </section>
        <section v-if="findings.facts.length" data-testid="map-facts">
          <h5>{{ $t('incidentsMap.details.facts') }}</h5>
          <ul><li v-for="f in findings.facts" :key="f.id">{{ codeText('facts', f.code, f.params) }}</li></ul>
        </section>
        <section v-if="findings.hypotheses.length" data-testid="map-hypotheses">
          <h5>{{ $t('incidentsMap.details.hypotheses') }}</h5>
          <ul>
            <li v-for="h in findings.hypotheses" :key="h.id">
              {{ codeText('hypotheses', h.code, h.params) }}
              <span class="faint">({{ $t(`incidentsMap.confidence.${confidenceLevel(h.confidence)}`) }})</span>
            </li>
          </ul>
        </section>
        <section v-if="findings.missing.length" data-testid="map-missing">
          <h5>{{ $t('incidentsMap.details.missing') }}</h5>
          <ul><li v-for="(m, i) in findings.missing" :key="i">{{ codeText('missing', m.code, m.params) }}</li></ul>
        </section>
        <p v-if="!hasFindings" class="faint" data-testid="map-details-none">{{ $t('incidentsMap.details.none') }}</p>
      </template>
    </template>
  </aside>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { te } from '../../../i18n'
import { codeText, confidenceLevel } from '../../../composables/incidentModel'
import { HEALTH_ICONS, VARIANT_ICONS, findingsFor, relationsOf, type MapModel, type MapNode } from '../../../composables/dependencyMap'
import type { Report } from '../../../types/incident'

const props = defineProps<{
  report?: Report | null
  /** Полная (несвёрнутая) модель: связи вершины считаются по ней */
  model: MapModel
  node: MapNode | null
}>()
const emit = defineEmits<{ (e: 'clear'): void; (e: 'expand', groupKey: string): void }>()
const { t } = useI18n()

const findings = computed(() => findingsFor(props.report, props.node?.key ?? ''))
const relations = computed(() => (props.node && props.node.variant !== 'group' ? relationsOf(props.model, props.node.key) : []))
const hasFindings = computed(() => findings.value.facts.length + findings.value.hypotheses.length + findings.value.missing.length + relations.value.length > 0)
const unknownText = computed(() => t(`incidentsMap.unknownReasons.${props.node?.unknown ?? 'other'}`))
/** Причина состояния словами; неизвестная причина не показывается «сырой» — просто опускается. */
function reasonLabel(reason: string | undefined): string {
  const key = `incidentsMap.reasons.${reason ?? ''}`
  return reason && te(key) ? t(key) : ''
}
const reasonText = computed(() => reasonLabel(props.node?.reason))
const groupLabel = computed(() => {
  const n = props.node
  if (!n) return ''
  const count = n.members?.length ?? 0
  return n.namespace ? t('incidentsMap.group.labelNs', { count, kind: n.kind, namespace: n.namespace }) : t('incidentsMap.group.label', { count, kind: n.kind })
})
</script>

<style scoped>
.details { min-width: 220px; max-width: 340px; padding: 10px 12px; border: 1px solid var(--line-strong, #45475a); border-radius: 8px; background: var(--bg-sunken, #181825); color: var(--text, #cdd6f4); font-size: 12px; }
header { display: flex; justify-content: space-between; gap: 8px; align-items: baseline; }
h4 { margin: 0; font-size: 14px; overflow-wrap: anywhere; }
h5 { margin: 10px 0 4px; font-size: 12px; color: var(--text-muted, #a6adc8); }
ul { margin: 0; padding-left: 16px; display: grid; gap: 3px; }
.meta, .faint, .empty { color: var(--text-muted, #a6adc8); }
.meta, .state, .note { margin: 4px 0; }
.icon { display: inline-block; width: 1.2em; font-weight: 700; }
.tag { margin-left: 6px; padding: 0 6px; border-radius: 4px; background: rgba(249, 226, 175, 0.18); color: var(--warn, #f9e2af); font-weight: 600; }
.link { background: none; border: 0; color: var(--accent, #89b4fa); cursor: pointer; font-size: 12px; padding: 0; }
.btn { margin-top: 6px; padding: 4px 10px; border: 1px solid var(--line-strong, #45475a); border-radius: 6px; background: transparent; color: var(--text, #cdd6f4); cursor: pointer; }
.link:focus-visible, .btn:focus-visible { outline: 2px solid var(--accent, #89b4fa); outline-offset: 2px; }
</style>
