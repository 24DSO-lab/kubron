<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { RuleStats } from '../../types/incident'

/** Статистика правил кластера и выгрузка (ADR-0034 §7). Оболочка. */
defineProps<{
  stats?: RuleStats
  canExport?: boolean
}>()
</script>

<template>
  <div class="intel-shell" data-shell="RuleStatsPanel"></div>
</template>
