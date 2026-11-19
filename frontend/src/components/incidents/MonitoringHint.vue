<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { MissingContext } from '../../types/incident'

/** Подсказка «с мониторингом диагноз точнее» по MissingContext metrics.* (ADR-0032 §7). Оболочка. */
defineProps<{
  missing?: MissingContext[]
}>()
</script>

<template>
  <div class="intel-shell" data-shell="MonitoringHint"></div>
</template>
