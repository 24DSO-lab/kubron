<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { ProblemSummary } from '../../types/incident'

/** Список открытых проблем (ADR-0033 §7). Оболочка. */
defineProps<{
  items?: ProblemSummary[]
}>()
</script>

<template>
  <div class="intel-shell" data-shell="ProblemList"></div>
</template>
