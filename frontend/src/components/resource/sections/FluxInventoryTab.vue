<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="flux-stub" />
</template>

<script lang="ts" setup>
import type { FluxObjectInfo } from '../../../types/flux'

// Заглушка задачи 0.2: вкладку ресурсов (inventory) заполняет задача U.3
defineProps<{ resource: Partial<FluxObjectInfo> }>()
</script>
