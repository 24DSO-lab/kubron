<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="pod-containers-cell">
    <span
      v-for="c in containers"
      :key="c.name"
      class="container-square"
      :class="`sq-${c.color}`"
      :title="c.name"
    />
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'

const props = defineProps<{ item: any; col: any }>()

const containers = computed(() => {
  const list = props.item?.containers ?? []
  return list.map((c: any) => {
    let color = 'red'
    if (c.isInit) color = 'gray'
    else if (c.ready) color = 'green'
    else if (c.state?.includes('wait') || c.state?.includes('Creat') || c.state?.includes('Pend') || c.state === 'waiting') color = 'yellow'
    else if (c.state === 'terminated' || c.state?.includes('Complet')) color = 'gray'
    return { name: c.name, color }
  })
})
</script>

<style scoped>
.pod-containers-cell { display: flex; gap: 4px; align-items: center; flex-wrap: wrap; }
.container-square {
  display: inline-block;
  width: 12px;
  height: 12px;
  border-radius: 2px;
  cursor: help;
}
.sq-green { background-color: var(--green); }
.sq-red   { background-color: var(--red); }
.sq-yellow{ background-color: var(--yellow); }
.sq-gray  { background-color: var(--text-gray); }
</style>
