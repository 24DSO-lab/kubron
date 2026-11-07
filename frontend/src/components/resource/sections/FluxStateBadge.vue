<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <span class="flux-stub">{{ state }}</span>
</template>

<script lang="ts" setup>
import type { FluxState } from '../../../types/flux'

// Заглушка задачи 0.2: бейдж состояния (цвета, перевод, доступность) заполняет задача U.3
defineProps<{ state: FluxState }>()
</script>
