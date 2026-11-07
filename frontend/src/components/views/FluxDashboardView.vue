<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="flux-stub" />
</template>

<script lang="ts" setup>
// Заглушка задачи 0.2: дашборд Flux (сводка по состояниям, проблемные объекты) заполняет задача U.9
withDefaults(defineProps<{ clusterName?: string }>(), { clusterName: '' })
defineEmits<{ (e: 'navigate', view: string): void }>()
</script>
