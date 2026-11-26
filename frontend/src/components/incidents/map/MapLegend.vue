<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <details class="legend" data-testid="map-legend">
    <summary>{{ $t('incidentsMap.legend.title') }}</summary>
    <div class="cols">
      <section>
        <h4>{{ $t('incidentsMap.legend.states') }}</h4>
        <ul>
          <li v-for="h in states" :key="h" :class="`h-${h}`"><span class="chip" aria-hidden="true">{{ HEALTH_ICONS[h] }}</span>{{ $t(`incidentsMap.health.${h}`) }}</li>
          <li class="v-placeholder"><span class="chip" aria-hidden="true">{{ VARIANT_ICONS.placeholder }}</span>{{ $t('incidentsMap.variant.placeholder') }}</li>
          <li class="v-unknown"><span class="chip" aria-hidden="true">{{ VARIANT_ICONS.unknown }}</span>{{ $t('incidentsMap.variant.unknown') }}</li>
          <li class="v-secret"><span class="chip" aria-hidden="true">{{ VARIANT_ICONS.secret }}</span>{{ $t('incidentsMap.variant.secret') }}</li>
          <li class="v-group"><span class="chip" aria-hidden="true">{{ VARIANT_ICONS.group }}</span>{{ $t('incidentsMap.variant.group') }}</li>
        </ul>
      </section>
      <section>
        <h4>{{ $t('incidentsMap.legend.edges') }}</h4>
        <ul>
          <li><span class="line s-ok" aria-hidden="true"></span>{{ $t('incidentsMap.legend.edgeSolid') }}</li>
          <li><span class="line s-broken" aria-hidden="true"></span>{{ $t('incidentsMap.legend.edgeBroken') }}</li>
          <li><span class="line s-missing" aria-hidden="true"></span>{{ $t('incidentsMap.legend.edgeMissing') }}</li>
          <li><span class="line s-unknown" aria-hidden="true"></span>{{ $t('incidentsMap.legend.edgeUnknown') }}</li>
        </ul>
        <h4>{{ $t('incidentsMap.legend.marks') }}</h4>
        <ul>
          <li><span class="mark cand" aria-hidden="true"></span>{{ $t('incidentsMap.candidate') }}</li>
          <li><span class="line s-chain" aria-hidden="true"></span>{{ $t('incidentsMap.legend.chain') }}</li>
        </ul>
      </section>
    </div>
  </details>
</template>

<script lang="ts" setup>
import { HEALTH_ICONS, VARIANT_ICONS, type KnownHealth } from '../../../composables/dependencyMap'

// Missing и Unknown в легенде показаны отдельными строками вариантов, поэтому здесь — только «обычные» состояния.
const states: KnownHealth[] = ['Healthy', 'Degraded', 'Progressing', 'Suspended']
</script>

<style scoped>
.legend { font-size: 12px; color: var(--text-muted, #a6adc8); }
summary { cursor: pointer; }
summary:focus-visible { outline: 2px solid var(--accent, #89b4fa); outline-offset: 2px; }
.cols { display: flex; flex-wrap: wrap; gap: 16px; margin-top: 6px; }
h4 { margin: 0 0 4px; font-size: 12px; color: var(--text, #cdd6f4); }
ul { list-style: none; margin: 0 0 6px; padding: 0; display: grid; gap: 3px; }
li { display: flex; align-items: center; gap: 6px; }
.chip { display: inline-grid; place-items: center; width: 18px; height: 18px; border: 1.5px solid var(--line-strong, #45475a); border-radius: 4px; font-weight: 700; }
.h-Healthy .chip { border-color: var(--ok, #a6e3a1); }
.h-Degraded .chip { border-color: var(--amber, #fab387); }
.h-Progressing .chip, .h-Suspended .chip { border-color: var(--accent, #89b4fa); }
.v-placeholder .chip { border-color: var(--danger, #f38ba8); border-style: dashed; }
.v-unknown .chip, .v-secret .chip { border-color: var(--text-faint, #9399b2); border-style: dotted; }
.v-group .chip { border-color: var(--line-strong, #45475a); box-shadow: 2px -2px 0 var(--line-strong, #45475a); }
.line { display: inline-block; width: 26px; border-top: 2px solid var(--text-faint, #7f849c); }
.line.s-broken { border-top-color: var(--amber, #fab387); border-top-width: 3px; }
.line.s-missing { border-top-style: dashed; border-top-color: var(--danger, #f38ba8); }
.line.s-unknown { border-top-style: dotted; }
.line.s-chain { border-top-color: var(--warn, #f9e2af); border-top-width: 3px; }
.mark.cand { display: inline-block; width: 18px; height: 12px; border-radius: 4px; box-shadow: 0 0 0 2px var(--warn, #f9e2af); }
</style>
