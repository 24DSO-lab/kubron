<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="nav-group">
    <div class="nav-group-header" :data-testid="`nav-group-${group.id}`" @click="emit('toggle', group.id)">
      <span class="nav-arrow" :class="{ open: expanded[group.id] }">›</span>
      <span class="nav-group-icon"></span>
      <span class="nav-group-title">{{ tl(group.title) }}</span>
    </div>
    <div v-show="expanded[group.id]" class="nav-group-items">
      <div
        v-for="item in group.items"
        :key="item.view"
        :class="['nav-item', { active: currentView === item.view }]"
        :data-testid="`nav-item-${item.view}`"
        @click="emit('navigate', item.view)"
      >
        <span class="nav-icon"></span> {{ tl(item.label) }}
      </div>
      <NavGroupNode
        v-for="child in visibleChildren"
        :key="child.id"
        :group="child"
        :expanded="expanded"
        :current-view="currentView"
        :available="available"
        @toggle="emit('toggle', $event)"
        @navigate="emit('navigate', $event)"
      />
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { tl } from '../../i18n'
import type { NavGroupId } from '../../resources/types'
import type { NavGroup } from '../../resources/navigation'

/** Рекурсивный узел группы бокового меню (поддерживает вложенные группы) */
const props = defineProps<{
  group: NavGroup
  expanded: Record<NavGroupId, boolean>
  currentView: string
  /** Выполненные условия `requires` (например, 'argocd') */
  available: ReadonlySet<string>
}>()

const emit = defineEmits<{
  (e: 'toggle', id: NavGroupId): void
  (e: 'navigate', view: string): void
}>()

const visibleChildren = computed(() =>
  props.group.children.filter(c => !c.requires || props.available.has(c.requires)),
)
</script>
