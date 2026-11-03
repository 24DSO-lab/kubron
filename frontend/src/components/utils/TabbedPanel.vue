<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="tabbed-panel" :style="panelStyle">
    <!-- Перетаскивание только за заголовок: иначе выделение текста в логах/терминале двигало бы панель -->
    <div class="panel-header" @pointerdown="startDrag" @pointermove="handleDrag" @pointerup="stopDrag"
      @pointercancel="stopDrag">
      <div class="tabs-container">
        <div class="tabs-list">
          <div v-for="tab in tabs" :key="tab.id" class="tab" :class="{ active: activeTabId === tab.id }"
            @click="activeTabId = tab.id">
            <span class="tab-icon">{{ tab.icon }}</span>
            <span class="tab-name">{{ tab.title }}</span>
            <button class="tab-close" @click.stop="closeTab(tab.id)">✕</button>
          </div>
        </div>
      </div>
      <button class="btn-close" @click="closeAllTabs">✕</button>
    </div>
    <div class="panel-content">
      <!-- Вкладки остаются смонтированными (v-show): редактор, терминал и логи не теряют состояние при переключении -->
      <template v-for="tab in tabs" :key="tab.id">
        <LogsView v-if="tab.type === 'logs'" v-show="activeTabId === tab.id" :tab="tab"
          :active="activeTabId === tab.id" />
        <YamlView v-else-if="tab.type === 'yaml'" v-show="activeTabId === tab.id" :tab="tab"
          :active="activeTabId === tab.id" @apply="emit('apply-yaml', $event)" />
        <TerminalView v-else-if="tab.type === 'terminal'" v-show="activeTabId === tab.id" :tab="tab"
          :active="activeTabId === tab.id" />
      </template>
      <div v-if="tabs.length === 0" class="no-tabs">
        <p>{{ $t('utils.tab.noTabs') }}</p>
      </div>
    </div>
    <div class="resize-handle" @pointerdown="startResize" @pointermove="handleResize" @pointerup="stopResize"
      @pointercancel="stopResize"></div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed, onMounted } from 'vue'
import editorWorker from 'monaco-editor/editor/editor.worker?worker'
import { globalTabbedPanel } from '../../composables/useTabbedPanel'
import LogsView from './LogsView.vue'
import YamlView from './YamlView.vue'
import TerminalView from './TerminalView.vue'

// Web worker для Monaco (используется YamlView)
if (typeof window !== 'undefined') {
  (window as any).MonacoEnvironment = {
    getWorker: function (_moduleId: string, _label: string) {
      return new editorWorker()
    }
  }
}

const emit = defineEmits<{ (e: 'close'): void; (e: 'apply-yaml', tabId: string): void }>()

const tabs = computed(() => globalTabbedPanel.tabs.value)
const activeTabId = computed({ get: () => globalTabbedPanel.activeTabId.value, set: (val) => { globalTabbedPanel.activeTabId.value = val } })
const closeTab = (tabId: string) => globalTabbedPanel.closeTab(tabId)
const closeAllTabs = () => globalTabbedPanel.closeAllTabs()

// ─── Положение и размер панели ───
const sidebarWidth = ref(200)
const panelX = ref(276)
const panelY = ref(window.innerHeight - 400)
const panelWidth = ref(window.innerWidth - sidebarWidth.value - 80)
const panelHeight = ref(400)
const isDragging = ref(false)
const isResizing = ref(false)
const dragStartX = ref(0)
const dragStartY = ref(0)
const dragStartLeft = ref(0)
const dragStartTop = ref(0)
const resizeStartX = ref(0)
const resizeStartY = ref(0)
const resizeStartWidth = ref(0)
const resizeStartHeight = ref(0)

const panelStyle = computed(() => ({ position: 'fixed' as const, left: `${panelX.value}px`, top: `${panelY.value}px`, width: `${panelWidth.value}px`, height: `${panelHeight.value}px` }))

const CODICON_STYLE_ID = 'kubron-codicon-font'

onMounted(() => {
  const sidebar = document.querySelector('.sidebar') as HTMLElement | null
  if (sidebar) sidebarWidth.value = sidebar.offsetWidth
  if (!document.getElementById(CODICON_STYLE_ID)) {
    const style = document.createElement('style')
    style.id = CODICON_STYLE_ID
    style.textContent = `@font-face { font-family: 'codicon'; src: url('${new URL('monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.ttf', import.meta.url).href}') format('truetype'); }`
    document.head.appendChild(style)
  }
})

// Заголовок не должен уходить за пределы окна, иначе панель нечем будет вернуть.
const HEADER_MIN_VISIBLE = 80
const HEADER_HEIGHT = 44

function clampPanel() {
  panelX.value = Math.min(Math.max(panelX.value, HEADER_MIN_VISIBLE - panelWidth.value), window.innerWidth - HEADER_MIN_VISIBLE)
  panelY.value = Math.min(Math.max(panelY.value, 0), window.innerHeight - HEADER_HEIGHT)
}

function startDrag(e: PointerEvent) {
  if (e.button !== 0) return
  // Вкладки и кнопки получают обычный click; захват указателя перенаправил бы его на заголовок.
  if ((e.target as HTMLElement).closest('button, .tab')) return
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  isDragging.value = true
  dragStartX.value = e.clientX
  dragStartY.value = e.clientY
  dragStartLeft.value = panelX.value
  dragStartTop.value = panelY.value
}

function stopDrag(e: PointerEvent) {
  if (!isDragging.value) return
  isDragging.value = false
  ;(e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId)
}

function handleDrag(e: PointerEvent) {
  if (!isDragging.value) return
  panelX.value = dragStartLeft.value + (e.clientX - dragStartX.value)
  panelY.value = dragStartTop.value + (e.clientY - dragStartY.value)
  clampPanel()
}

function startResize(e: PointerEvent) {
  if (e.button !== 0) return
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  isResizing.value = true
  resizeStartX.value = e.clientX
  resizeStartY.value = e.clientY
  resizeStartWidth.value = panelWidth.value
  resizeStartHeight.value = panelHeight.value
  e.preventDefault()
}

function stopResize(e: PointerEvent) {
  if (!isResizing.value) return
  isResizing.value = false
  ;(e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId)
}

function handleResize(e: PointerEvent) {
  if (!isResizing.value) return
  panelWidth.value = Math.max(400, resizeStartWidth.value + (e.clientX - resizeStartX.value))
  panelHeight.value = Math.max(250, resizeStartHeight.value + (e.clientY - resizeStartY.value))
}
</script>

<style scoped>
.tabbed-panel {
  position: fixed;
  background: #1a1b26;
  border: 1px solid #3b3d5c;
  border-radius: 4px;
  display: flex;
  flex-direction: column;
  z-index: 100;
  overflow: hidden;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
}

.panel-header {
  display: flex;
  align-items: center;
  padding: 0;
  border-bottom: 1px solid #3b3d5c;
  background: #2a2b3a;
  min-height: 44px;
  user-select: none;
  cursor: move;
  touch-action: none;
}

.tabs-container {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: 1;
  padding: 0 8px;
  overflow-x: auto;
}

.tabs-list {
  display: flex;
  gap: 4px;
  align-items: center;
}

.tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: #1e1f2e;
  border: 1px solid #3b3d5c;
  border-radius: 3px 3px 0 0;
  color: #999;
  cursor: pointer;
  transition: all 0.2s;
  white-space: nowrap;
  min-width: 140px;
}

.tab:hover {
  background: #252a3a;
}

.tab.active {
  background: #2a2b3a;
  border-bottom-color: #2a2b3a;
  color: #e0e0e0;
}

.tab-icon {
  font-size: 14px;
}

.tab-name {
  font-size: 12px;
  font-weight: 500;
}

.tab-close {
  background: none;
  border: none;
  color: #666;
  cursor: pointer;
  padding: 0 4px;
  transition: color 0.2s;
  font-size: 12px;
  margin-left: auto;
}

.tab-close:hover {
  color: #999;
}

.btn-close {
  background: none;
  border: none;
  color: #666;
  cursor: pointer;
  padding: 0 12px;
  font-size: 18px;
  transition: color 0.2s;
}

.btn-close:hover {
  color: #fff;
}

.panel-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.no-tabs {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #666;
}

.resize-handle {
  position: absolute;
  bottom: 0;
  right: 0;
  width: 20px;
  height: 20px;
  cursor: se-resize;
  touch-action: none;
  background: linear-gradient(135deg, transparent 50%, #3b3d5c 50%);
  opacity: 0.5;
}

.resize-handle:hover {
  opacity: 1;
}
</style>
