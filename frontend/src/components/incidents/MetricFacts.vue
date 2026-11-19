<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { Fact } from '../../types/incident'

/** Факты metric.* со значением, единицей, окном и агрегатом (ADR-0032 §6). Оболочка. */
defineProps<{
  facts?: Fact[]
}>()
</script>

<template>
  <div class="intel-shell" data-shell="MetricFacts"></div>
</template>
