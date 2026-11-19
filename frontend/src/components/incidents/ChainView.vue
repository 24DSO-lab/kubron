<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script setup lang="ts">
import type { Chain, Hypothesis } from '../../types/incident'

/** Компактная цепочка зависимостей Report.chains (ADR-0031 §8). Оболочка: логика — Фаза 1. */
defineProps<{
  chains?: Chain[]
  hypotheses?: Hypothesis[]
}>()
</script>

<template>
  <div class="intel-shell" data-shell="ChainView"></div>
</template>
