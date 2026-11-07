<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="flux-stub" />
</template>

<script lang="ts" setup>
import type { FluxObjectInfo } from '../../../types/flux'

// Заглушка задачи 0.2: секцию источника (sourceRef) заполняет задача U.3
defineProps<{ resource: Partial<FluxObjectInfo> }>()
</script>
