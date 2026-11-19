<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { ProblemDetail } from '../../types/incident'

/** Экран проблемы: участники и ручные действия (ADR-0033 §7). Оболочка. */
defineProps<{
  problem?: ProblemDetail
  canOperate?: boolean
}>()
</script>

<template>
  <div class="intel-shell" data-shell="ProblemView"></div>
</template>
