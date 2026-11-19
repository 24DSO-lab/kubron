<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { CorrelationResult } from '../../types/incident'

/** Блок «Возможный общий источник» (ADR-0033 §5). Оболочка. */
defineProps<{
  correlation?: CorrelationResult
  problemId?: string
}>()
</script>

<template>
  <div class="intel-shell" data-shell="CorrelationBlock"></div>
</template>
