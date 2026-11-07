<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="flux-stub" />
</template>

<script lang="ts" setup>
import type { FluxKustomizationInfo, FluxHelmReleaseInfo } from '../../types/flux'

// Заглушка задачи 0.2: диалог удаления с последствиями и подтверждением вводом имени заполняет задача U.10
defineProps<{ object: FluxKustomizationInfo | FluxHelmReleaseInfo }>()
defineEmits<{ (e: 'close'): void; (e: 'deleted'): void }>()
</script>
