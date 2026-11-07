<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <aside class="sidebar" :style="{ width: width + 'px' }">
    <!-- Cluster name -->
    <div class="sidebar-cluster-name" v-if="clusterName">
      <span class="cluster-name-text">{{ clusterName }}</span>
    </div>

    <!-- Navigation Tree: пункты строятся из resources/navigation.ts -->
    <nav class="nav-tree">
      <div :class="['nav-item', { active: currentView === 'overview' }]" @click="emit('navigate', 'overview')">
        <span class="nav-icon"></span> {{ $t('nav.overview') }}
      </div>

      <template v-for="entry in visibleEntries" :key="entry.type === 'group' ? entry.id : entry.view">
        <div
          v-if="entry.type === 'item'"
          :class="['nav-item', { active: currentView === entry.view }]"
          @click="emit('navigate', entry.view)"
        >
          <span class="nav-icon"></span> {{ tl(entry.label) }}
        </div>

        <NavGroupNode
          v-else
          :group="entry"
          :expanded="expanded"
          :current-view="currentView"
          :available="available"
          @toggle="toggle"
          @navigate="emit('navigate', $event)"
        />
      </template>
 <CustomResourcesNav :current-view="currentView" @navigate="emit('navigate', $event)" />

      <!-- Settings -->
      <div class="nav-separator"></div>
      <div :class="['nav-item', { active: currentView === 'settings' }]" @click="emit('navigate', 'settings')">
        <span class="nav-icon">⚙️</span> {{ $t('nav.settings') }}
      </div>
      <div v-if="capabilities.teams" :class="['nav-item', { active: currentView === 'teams' }]"
        @click="emit('navigate', 'teams')">
        <span class="nav-icon">🧩</span> {{ $t('nav.teams') }}
      </div>
      <div v-if="can(Perm.UsersManage)" :class="['nav-item', { active: currentView === 'users' }]"
        @click="emit('navigate', 'users')">
        <span class="nav-icon">👥</span> {{ $t('nav.users') }}
      </div>
      <div v-if="can(Perm.AuditRead)" :class="['nav-item', { active: currentView === 'audit' }]"
        @click="emit('navigate', 'audit')">
        <span class="nav-icon">📜</span> {{ $t('nav.audit') }}
      </div>
    </nav>

    <!-- Namespace selector -->
    <div class="ns-selector">
      <label class="ns-label-text">{{ $t('nav.namespace') }}</label>
      <NamespaceSelect
        :modelValue="selectedNamespaces"
        :namespaces="namespaces"
        :noAccess="failedNamespaces"
        @update:modelValue="emit('nsChange', $event)"
      />
    </div>
    <!-- Ручка ширины на правой границе: мышь/touch или стрелки ← → -->
  <div
    class="sidebar-resizer"
    role="separator"
    aria-orientation="vertical"
    :aria-label="$t('nav.resizeSidebar')"
    tabindex="0"
    @pointerdown="startResize"
    @keydown="onResizerKey"
    @dblclick="setWidth(SIDEBAR_DEFAULT_WIDTH, true)"
  ></div>
  </aside>
</template>

<script lang="ts" setup>
import { computed, reactive, onBeforeUnmount } from 'vue'
import type { NamespaceInfo } from '../types'
import type { NavGroupId } from '../resources/types'
import { NAV_GROUPS, buildNav, type NavEntry } from '../resources/navigation'
import { tl } from '../i18n'
import NavGroupNode from './nav/NavGroupNode.vue'
import NamespaceSelect from './NamespaceSelect.vue'
import { useListFailures } from '../composables/useListFailures'
import { useArgo } from '../composables/useArgo'
import { useFlux } from '../composables/useFlux'
import type { FluxControllers } from '../types/flux'
import { can } from '../composables/useAuth'
import { useCapabilities } from '../composables/useCapabilities'
import { Perm } from '../types/auth'
import CustomResourcesNav from './nav/CustomResourcesNav.vue'
import { SIDEBAR_DEFAULT_WIDTH, useSidebarWidth } from '../composables/useSidebarWidth'

const { width, setWidth } = useSidebarWidth()
const { capabilities } = useCapabilities()

let dragStartX = 0
let dragStartWidth = 0

function startResize(e: PointerEvent) {
  dragStartX = e.clientX
  dragStartWidth = width.value
  const el = e.currentTarget as HTMLElement
  el.setPointerCapture(e.pointerId)
  el.addEventListener('pointermove', onResizeMove)
  el.addEventListener('pointerup', stopResize)
  el.addEventListener('pointercancel', stopResize)
  document.body.style.userSelect = 'none'
  document.body.style.cursor = 'col-resize'
  e.preventDefault()
}

// Меню слева: тянем вправо — шире
function onResizeMove(e: PointerEvent) {
  setWidth(dragStartWidth + (e.clientX - dragStartX))
}

function stopResize(e: PointerEvent) {
  const el = e.currentTarget as HTMLElement
  el.removeEventListener('pointermove', onResizeMove)
  el.removeEventListener('pointerup', stopResize)
  el.removeEventListener('pointercancel', stopResize)
  if (el.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId)
  document.body.style.userSelect = ''
  document.body.style.cursor = ''
  setWidth(width.value, true) // запоминаем итоговую ширину
}

function onResizerKey(e: KeyboardEvent) {
  const step = e.shiftKey ? 40 : 10
  if (e.key === 'ArrowRight') { setWidth(width.value + step, true); e.preventDefault() }
  if (e.key === 'ArrowLeft') { setWidth(width.value - step, true); e.preventDefault() }
}


defineProps<{
  currentView: string
  selectedNamespaces: string[]
  namespaces: NamespaceInfo[]
  clusterName?: string
}>()

const emit = defineEmits<{
  (e: 'navigate', view: string): void
  (e: 'nsChange', ns: string[]): void
}>()

const { failedNamespaces } = useListFailures()

const navEntries = buildNav()

// Условия `requires` у групп меню: наличие Argo CD и Flux в кластере
const { argoInstalled } = useArgo()
const { fluxInstalled, fluxStatus } = useFlux()
const available = computed(() => {
  const set = new Set<string>()
  if (argoInstalled.value) set.add('argocd')
  if (fluxInstalled.value) set.add('flux')
  return set
})

/** Подгруппа Flux показывается, только если в кластере есть её контроллер (Flux можно поставить частично) */
const FLUX_SUBGROUP_CONTROLLER: Partial<Record<NavGroupId, keyof FluxControllers>> = {
  'flux-sources': 'source',
  'flux-notifications': 'notification',
  'flux-images': 'image',
}
const withFluxControllers = (entry: NavEntry): NavEntry =>
  entry.type === 'group' && entry.id === 'flux'
    ? {
        ...entry,
        children: entry.children.filter(c => {
          const need = FLUX_SUBGROUP_CONTROLLER[c.id]
          return !need || fluxStatus.value.controllers[need]
        }),
      }
    : entry

const visibleEntries = computed(() =>
  navEntries
    .filter(e => e.type === 'item' || !e.requires || available.value.has(e.requires))
    .map(withFluxControllers),
)

const expanded = reactive(
  Object.fromEntries(Object.entries(NAV_GROUPS).map(([id, g]) => [id, g.expanded])) as Record<NavGroupId, boolean>,
)

function toggle(group: NavGroupId) {
  expanded[group] = !expanded[group]
}

onBeforeUnmount(() => {
  document.body.style.userSelect = ''
  document.body.style.cursor = ''
})

</script>

<style scoped>
.sidebar { position: relative; }
.sidebar-resizer {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 6px;
  cursor: col-resize;
  z-index: 2;
  touch-action: none;
}
.sidebar-resizer:hover,
.sidebar-resizer:focus-visible,
.sidebar-resizer:active {
  background: rgba(139, 92, 246, 0.45);
  outline: none;
}
</style>