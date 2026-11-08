<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="detail-panel" :class="{ 'detail-expanded': expanded }" :style="panelStyle">
    <!-- Ручка ширины на левой границе: перетаскивание мышью или стрелки ← → с клавиатуры -->
    <div
      v-if="!expanded"
      class="detail-resizer"
      role="separator"
      aria-orientation="vertical"
      :aria-label="$t('panels.detail.resize')"
      tabindex="0"
      @pointerdown="startResize"
      @keydown="onResizerKey"
      @dblclick="setWidth(DETAIL_DEFAULT_WIDTH, true)"
    ></div>
    <!-- Header with title and action menu -->
    <div class="detail-header">
      <span class="rd-label">{{ resourceType }}</span> 
      <div class="detail-actions-bar">
        <!-- Custom action menu items (slot or prop-based) -->
        <slot name="actions" :resource="resource"></slot>

        <!-- Развернуть панель на всю область контента / вернуть ширину -->
        <button
          class="btn-action btn-expand"
          :title="expanded ? $t('panels.detail.restoreWidth') : $t('panels.detail.expand')"
          :aria-label="expanded ? $t('panels.detail.restoreWidth') : $t('panels.detail.expand')"
          :aria-pressed="expanded"
          @click="toggleExpanded"
        >{{ expanded ? '⤡' : '⤢' }}</button>

        <!-- Edit button -->
        <button v-if="showEdit !== false" class="btn-action" @click="emit('edit')" :title="$t('common.edit')">
          <K8sIcon name="edit" :alt="$t('common.edit')" size="sm" />
        </button>

        <!-- Delete button -->
        <button v-if="showDelete !== false" class="btn-action" data-testid="resource-delete" @click="emit('delete')" :title="$t('panels.detail.deleteResource')">
          <K8sIcon name="delete" :alt="$t('common.delete')" size="sm" />
        </button>

        <!-- Close button -->
        <button class="btn-action" data-testid="resource-detail-close" @click="emit('close')" :title="$t('common.close')">
          <K8sIcon name="close" :alt="$t('common.close')" size="sm" />
        </button>
      </div>
    </div>

    <!-- Meta info (namespace, age, etc) -->
    <div v-if="showMetaInfo" class="detail-meta">
      <div class="meta-row">
        <span class="meta-label">{{ $t('panels.detail.name') }}</span>
        <div class="name-with-copy">
          {{ resource.name }}
          <button class="btn-copy" @click="copyToClipboard(resource.name)" :title="$t('panels.detail.copyName')">
            <K8sIcon name="copy" :alt="$t('common.copy')" size="sm" />
          </button>
        </div>
      </div>
      <div v-if="resource.namespace" class="meta-row">
        <span class="meta-label">{{ $t('panels.detail.namespace') }}</span>
        <a class="meta-link" @click.prevent="onNamespaceClick">
          {{ resource.namespace }}
        </a>
      </div>
      <div v-if="resource.age" class="meta-row">
        <span class="meta-label">{{ $t('panels.detail.age') }}</span>
        <div class="age-display">
          <div class="age-relative">{{ formatAgeRelative(resource.age) }}</div>
          <div class="age-absolute">{{ formatAgeAbsolute(resource.created) }}</div>
        </div>
      </div>
    </div>

    <!-- Tabs -->
    <div v-if="tabs.length > 0" class="tabs flex space-x-4 border-b border-gray-700 mb-4">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        :class="['tab', { active: activeTab === tab.id }]"
        @click="activeTab = tab.id"
      >
        {{ tl(tab.label) }}
      </button>
    </div>

    <!-- Tab content -->
    <div class="detail-content">
      <!-- Properties tab -->
      <div v-if="activeTab === 'properties'" class="detail-section text-gray-300">
        <slot name="properties" :resource="resource"></slot>
      </div>

      <!-- Events tab -->
      <div v-if="activeTab === 'events'" class="detail-section text-gray-300">
        <slot name="events" :resource="resource"></slot>
      </div>

      <!-- Additional custom tabs -->
      <template v-for="tab in customTabs" :key="tab.id">
        <div v-if="activeTab === tab.id" class="detail-section text-gray-300">
          <slot :name="`tab-${tab.id}`" :resource="resource"></slot>
        </div>
      </template>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { tl } from '../../i18n'
import { formatDateTime } from '../../i18n/format'
import K8sIcon from '../icons/K8sIcon.vue'
import { DETAIL_DEFAULT_WIDTH, useDetailPanelSize } from '../../composables/useDetailPanelSize'
import { useSidebarWidth } from '../../composables/useSidebarWidth'
const { t } = useI18n()
const { width: sidebarWidth } = useSidebarWidth()

const panelStyle = computed(() => {
  if (expanded.value) return { width: 'auto', left: `${sidebarWidth.value}px` }
  return { width: `${width.value}px` }
})

interface DetailTab {
  id: string
  label: string
}

const props = defineProps<{
  resource: any
  resourceIcon?: string
  resourceType?: string
  customTabs?: DetailTab[]
  /** false — скрыть кнопку Edit (для нередактируемых сущностей: helm, port forward и т.п.) */
  showEdit?: boolean
  /** false — скрыть кнопку Delete */
  showDelete?: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'edit'): void
  (e: 'delete'): void
  (e: 'namespaceClick', namespace: string): void
}>()

const activeTab = ref('properties')

// ─── Ширина панели ───
const { width, expanded, setWidth, toggleExpanded, collapse } = useDetailPanelSize()

// /** В развёрнутом виде панель занимает всё правее бокового меню */
// const panelStyle = computed(() => {
//   if (expanded.value) {
//     const sidebar = document.querySelector('.sidebar') as HTMLElement | null
//     return { width: 'auto', left: `${sidebar?.offsetWidth ?? 0}px` }
//   }
//   return { width: `${width.value}px` }
// })

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
  e.preventDefault()
}

// Панель прижата к правому краю: тянем влево — шире
function onResizeMove(e: PointerEvent) {
  setWidth(dragStartWidth + (dragStartX - e.clientX))
}

function stopResize(e: PointerEvent) {
  const el = e.currentTarget as HTMLElement
  el.removeEventListener('pointermove', onResizeMove)
  el.removeEventListener('pointerup', stopResize)
  el.removeEventListener('pointercancel', stopResize)
  if (el.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId)
  document.body.style.userSelect = ''
  setWidth(width.value, true) // запоминаем итоговую ширину
}

function onResizerKey(e: KeyboardEvent) {
  const step = e.shiftKey ? 80 : 20
  if (e.key === 'ArrowLeft') { setWidth(width.value + step, true); e.preventDefault() }
  if (e.key === 'ArrowRight') { setWidth(width.value - step, true); e.preventDefault() }
}

// Режим «развернуть» относится к открытой панели: после закрытия следующая открывается обычной ширины
onBeforeUnmount(() => {
  collapse()
  document.body.style.userSelect = ''
})

const showMetaInfo = computed(() => {
  return props.resource && props.resource.name
})

const tabs = computed<DetailTab[]>(() => {
  const baseTabs: DetailTab[] = [
    { id: 'properties', label: t('panels.detail.properties') },
    { id: 'events', label: t('panels.detail.events') }
  ]
  if (props.customTabs) {
    return [...baseTabs, ...props.customTabs]
  }
  return baseTabs
})

function onNamespaceClick() {
  if (props.resource?.namespace) {
    emit('namespaceClick', props.resource.namespace)
  }
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text).catch(err => {
    console.error('Failed to copy:', err)
  })
}

function formatAgeRelative(age: string): string {
  return age || '—'
}

function formatAgeAbsolute(created: string): string {
  if (!created) return ''
  try {
    const date = new Date(created)
    return formatDateTime(date) || created
  } catch {
    return created
  }
}
</script>

<style scoped>
.detail-panel {
  /* position/right/top/bottom/width заданы глобально (assets/style.css), ширину переопределяет :style */
  background: #1a1b2e;
  color: #d4d4d4;
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.detail-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid #2a2b40;
  background: #1a1b2e;
  gap: 12px;
  flex-shrink: 0;
}

.detail-header h3 {
  margin: 0;
  flex: 1;
  color: white;
  font-size: 1.1rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  display: flex;
  align-items: center;
  gap: 8px;
}

.resource-type-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 4px;
  background: #2a2b40;
  border: 1px solid #35365a;
  color: #868d8a;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.03em;
  line-height: 1.4;
  flex-shrink: 0;
}

.detail-actions-bar {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.detail-meta {
  padding: 12px 16px;
  border-bottom: 1px solid #2a2b40;
  background: rgba(255, 255, 255, 0.02);
  flex-shrink: 0;
}

.meta-row {
  display: grid;
  grid-template-columns: auto 1fr;
  align-items: baseline;
  margin-bottom: 6px;
  gap: 12px;
  text-align: left;
  font-size: 0.875rem;
}

.meta-row:last-child {
  margin-bottom: 0;
}

.meta-label {
  font-weight: bold;
  color: #d4d4d4;
  white-space: nowrap;
}

.tabs {
  overflow-x: auto;
  flex-shrink: 0;
  padding: 0 16px;
}

.tab {
  padding: 8px 16px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: #868d8a;
  cursor: pointer;
  font-size: 0.875rem;
  transition: color 0.15s, border-color 0.15s;
}

.tab:hover {
  color: #d4d4d4;
}

.tab.active {
  border-bottom-color: #3b82f6;
  color: white;
}

.detail-content {
  flex: 1;
  overflow-y: auto;
  padding: 0 16px 16px 16px;
}

.detail-section {
  padding: 8px 0;
}

.btn-close {
  padding: 6px 10px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: white;
  cursor: pointer;
  font-size: 1rem;
  transition: all 0.15s;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  height: 32px;
}

.btn-close:hover {
  background: rgba(255, 255, 255, 0.05);
}

.icon-close {
  width: 28px;
  height: 28px;
  filter: invert(1);
  opacity: 0.8;
}

.btn-close:hover .icon-close {
  opacity: 1;
}

.btn-action {
  padding: 6px 10px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: white;
  cursor: pointer;
  font-size: 1rem;
  transition: all 0.15s;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  height: 32px;
}

.btn-action:hover {
  background: rgba(255, 255, 255, 0.1);
}

.btn-action.btn-danger {
  color: #ef4444;
}

.btn-action.btn-danger:hover {
  background: rgba(239, 68, 68, 0.2);
}

.btn-icon {
  width: 24px;
  height: 24px;
  display: inline-block;
  vertical-align: middle;
  filter: invert(1);
  opacity: 0.85;
  transition: opacity 0.15s;
}

.btn-action:hover .btn-icon {
  opacity: 1;
}

.btn-action :deep(.k8s-icon) {
  opacity: 0.85;
}

.btn-action:hover :deep(.k8s-icon) {
  opacity: 1;
}

.btn-action.btn-danger :deep(.k8s-icon) {
  filter: invert(31%) sepia(91%) saturate(6000%) hue-rotate(350deg) brightness(95%) contrast(90%);
  opacity: 0.9;
}

.btn-action.btn-danger:hover :deep(.k8s-icon) {
  opacity: 1;
}

.meta-link {
  color: #60a5fa !important;
  cursor: pointer !important;
  text-decoration: underline;
  text-decoration-color: #60a5fa;
  text-decoration-thickness: 1px;
  text-underline-offset: 2px;
  transition: all 0.15s ease;
  display: inline-block;
  background: none !important;
  border: none !important;
  padding: 0 !important;
  font: inherit !important;
  pointer-events: auto !important;
  user-select: none;
}

.meta-link:hover {
  color: #93c5fd !important;
  text-decoration-color: #93c5fd;
}

.meta-link:active {
  color: #3b82f6 !important;
  text-decoration-color: #3b82f6;
}

.meta-link:focus {
  outline: 1px solid #60a5fa;
  outline-offset: 2px;
}

.name-with-copy {
  display: flex;
  align-items: center;
  gap: 8px;
}

.btn-copy {
  padding: 4px 6px;
  background: transparent;
  border: none;
  border-radius: 3px;
  color: #d4d4d4;
  cursor: pointer;
  font-size: 0.9rem;
  transition: all 0.15s;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  height: 32px;
  flex-shrink: 0;
}

.icon-inline {
  width: 10px;
  height: 10px;
  stroke: currentColor;
  filter: invert(1);
  opacity: 0.8;
}

.btn-copy:hover .icon-inline {
  opacity: 1;
}

.btn-copy:active {
  background: rgba(59, 130, 246, 0.1);
}

.flex { display: flex; }
.space-x-4 { gap: 1rem; }
.border-b { border-bottom: 1px solid; }
.border-gray-700 { border-bottom-color: #2a2b40; }
.mb-4 { margin-bottom: 1rem; }
.text-gray-300 { color: #d4d4d4; }

.age-display {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.age-relative {
  font-weight: 500;
  color: #d4d4d4;
}

.age-absolute {
  font-size: 0.8rem;
  color: #868d8a;
}
</style>
<style scoped>
.detail-resizer {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 6px;
  cursor: col-resize;
  z-index: 2;
  touch-action: none;
}
.detail-resizer:hover,
.detail-resizer:focus-visible,
.detail-resizer:active {
  background: rgba(139, 92, 246, 0.45);
  outline: none;
}
.detail-expanded {
  box-shadow: none;
}
.btn-expand {
  font-size: 1rem;
  line-height: 1;
  color: #d4d4d4;
}
</style>
