<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <span class="cl-avatar" :style="boxStyle" aria-hidden="true">
    <svg v-if="glyph" class="cl-avatar-glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
      stroke-linecap="round" stroke-linejoin="round">
      <path v-for="d in glyph.paths" :key="d" :d="d" />
    </svg>
    <template v-else>{{ initials }}</template>
    <i v-if="showEnv && env" class="cl-avatar-env" :style="{ background: env.color }"></i>
  </span>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import './cluster.css'
import {
  DEFAULT_CLUSTER_COLOR, clusterInitials, findEnvironment, findGlyph, withAlpha,
} from './clusterStyle'

// Значок кластера: монограмма или контурный значок на тонированной плитке.
// Устаревшие эмодзи из старых метаданных не рисуются — вместо них монограмма.
const props = withDefaults(defineProps<{
  name: string
  icon?: string
  color?: string
  environment?: string
  size?: number
  /** Показывать точку окружения (prod/stage/…) в углу */
  showEnv?: boolean
}>(), {
  icon: '',
  color: '',
  environment: '',
  size: 40,
  showEnv: false,
})

const glyph = computed(() => findGlyph(props.icon))
const env = computed(() => findEnvironment(props.environment))
const initials = computed(() => clusterInitials(props.name))

const boxStyle = computed(() => {
  const accent = props.color || DEFAULT_CLUSTER_COLOR
  return {
    width: `${props.size}px`,
    height: `${props.size}px`,
    borderRadius: `${Math.max(6, Math.round(props.size * 0.26))}px`,
    fontSize: `${Math.round(props.size * 0.36)}px`,
    // В светлой теме пастельный цвет подписи затемняется токеном (в тёмной — 100%, цвет без изменений)
    color: `color-mix(in srgb, ${accent} var(--avatar-accent-pct), black)`,
    background: withAlpha(accent, 0.14),
    borderColor: withAlpha(accent, 0.4),
  }
})
</script>
