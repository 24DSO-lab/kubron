<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="entries.length" class="rd-wide cm-data">
    <div v-for="[key, val] in entries" :key="key" class="cm-data-item">
      <div class="cm-data-header">
        <div class="cm-data-key">{{ key }}</div>
      </div>
      <div :ref="el => setEditorContainer(key, el as HTMLDivElement | null)" class="cm-editor-container"></div>
      <div class="cm-data-footer">
        <button
          class="cm-data-save"
          :disabled="saving[key] || !dirty[key]"
          @click="saveKey(key)"
        >
          {{ saving[key] ? $t('common.saving') : $t('common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, onMounted, onUpdated, reactive } from 'vue'

import 'monaco-editor'
import * as monaco from 'monaco-editor/editor/editor.api'
import editorWorker from 'monaco-editor/editor/editor.worker?worker'

// Configure Monaco web workers
if (typeof window !== 'undefined') {
  (window as any).MonacoEnvironment = {
    getWorker: function (_moduleId: string, _label: string) {
      return new editorWorker()
    }
  }
}
import { callApp } from '../../../resources/wails'
import { useNotifications } from '../../../composables/useNotifications'
import { t } from '../../../i18n'
import { errorText } from '../../../composables/errorText'

const props = defineProps<{ resource: any }>()
const entries = computed(() => Object.entries(props.resource?.data ?? {}) as [string, string][])

const { notifySuccess, notifyError } = useNotifications()

function detectLanguage(val: string): string {
  const trimmed = val.trim()
  if (!trimmed) return 'plaintext'
  // JSON: starts with { or [
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { JSON.parse(trimmed); return 'json' } catch { /* not valid JSON */ }
  }
  // YAML: key: value lines, lists with -, comments with #
  const yamlLine = /^(?:[^\s#][^:]*:\s?.*|[-#]\s?.*)$/m
  const yamlLike = (trimmed.match(/^[^\s#][^:]*:/gm) || []).length >= 1
  if (yamlLike) return 'yaml'
  // ENV: KEY=value lines
  const envLines = trimmed.split('\n').filter(l => l.trim() && !l.trim().startsWith('#'))
  if (envLines.length > 0 && envLines.every(l => /^[A-Za-z_][A-Za-z0-9_]*=/.test(l.trim()))) return 'ini'
  return 'plaintext'
}

const editorContainers = new Map<string, HTMLDivElement>()
const editors = new Map<string, monaco.editor.IStandaloneCodeEditor>()
const saving = reactive<Record<string, boolean>>({})
const dirty = reactive<Record<string, boolean>>({})

function setEditorContainer(key: string, el: HTMLDivElement | null) {
  if (el) {
    editorContainers.set(key, el)
  } else {
    editorContainers.delete(key)
  }
}

function createOrUpdateEditors() {
  for (const [key, val] of entries.value) {
    const container = editorContainers.get(key)
    if (!container) continue

    const existing = editors.get(key)
    if (existing) {
      // Update value if changed externally (not by user editing)
      const currentValue = existing.getValue()
      if (currentValue !== val && !dirty[key]) {
        existing.setValue(val)
      }
      continue
    }

    const editor = monaco.editor.create(container, {
      value: val,
      language: detectLanguage(val),
      theme: 'vs-dark',
      automaticLayout: true,
      minimap: { enabled: false, scale: 1, showSlider: 'mouseover' },
      scrollBeyondLastLine: false,
      wordWrap: 'on',
      fontSize: 13,
      fontFamily: "'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', monospace",
      lineNumbers: 'on',
      folding: true,
      readOnly: false,
      scrollbar: {
        verticalScrollbarSize: 8,
        horizontalScrollbarSize: 8,
        useShadows: false
      }
    })
    editors.set(key, editor)

    // Auto-resize based on line count
    const updateHeight = () => {
      const lineCount = editor.getModel()?.getLineCount() ?? 1
      // Min height for 1 line, max 300px (about 15 lines at 20px/line)
      const lineHeight = 20
      const padding = 16 // top + bottom padding
      const minLines = 3
      const maxLines = 15
      const lines = Math.max(minLines, Math.min(maxLines, lineCount))
      const height = lines * lineHeight + padding
      container.style.height = `${height}px`
    }
    updateHeight()
    editor.onDidChangeModelContent(() => updateHeight())

    editor.onDidChangeModelContent(() => {
      dirty[key] = editor.getValue() !== (props.resource?.data?.[key] ?? '')
    })
  }

  // Dispose editors for removed keys
  for (const [key, editor] of editors) {
    if (!entries.value.find(([k]) => k === key)) {
      editor.dispose()
      editors.delete(key)
      delete saving[key]
      delete dirty[key]
    }
  }
}

async function saveKey(key: string) {
  const editor = editors.get(key)
  if (!editor) return

  const namespace = props.resource?.namespace
  const name = props.resource?.name
  if (!namespace || !name) {
    notifyError(t('sections.configMapData.noTarget'))
    return
  }

  const newValue = editor.getValue()
  const updatedData = { ...props.resource?.data, [key]: newValue }

  saving[key] = true
  try {
    await callApp('UpdateConfigMap', namespace, name, updatedData)
    dirty[key] = false
    // Update the editor value to the saved value
    const editor = editors.get(key)
    if (editor) {
      editor.setValue(newValue)
    }
    notifySuccess(t('sections.shared.keySaved', { key }))
  } catch (e: any) {
    notifyError(errorText(e) || t('sections.shared.saveFailed'))
  } finally {
    saving[key] = false
  }
}

let codiconStyle: HTMLStyleElement | null = null

onMounted(() => {
  // Monaco codicon font (required for proper editor rendering)
  codiconStyle = document.createElement('style')
  codiconStyle.textContent = `@font-face { font-family: 'codicon'; src: url('${new URL('monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.ttf', import.meta.url).href}') format('truetype'); }`
  document.head.appendChild(codiconStyle)

  nextTick(() => createOrUpdateEditors())
})

onUpdated(() => {
  createOrUpdateEditors()
})

onBeforeUnmount(() => {
  for (const editor of editors.values()) {
    editor.dispose()
  }
  editors.clear()
  if (codiconStyle) {
    document.head.removeChild(codiconStyle)
    codiconStyle = null
  }
})
</script>

<style scoped>
.cm-data {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.cm-data-item {
  border: 1px solid var(--line);
  border-radius: 4px;
  overflow: hidden;
  background: rgba(var(--tint-rgb), 0.02);
}

.cm-data-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: var(--panel);
  border-bottom: 1px solid var(--line);
}

.cm-data-key {
  font-weight: 600;
  color: var(--blue);
  font-size: 0.85rem;
}

.cm-data-save {
  padding: 4px 12px;
  font-size: 0.8rem;
  font-weight: 500;
  color: #fff;
  background: var(--blue-strong);
  border: none;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.15s;
}

.cm-data-save:hover:not(:disabled) {
  background: var(--blue-deep);
}

.cm-data-save:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.cm-editor-container {
  min-height: 60px;
}

.cm-data-footer {
  display: flex;
  justify-content: flex-end;
  padding: 8px 12px;
  background: var(--panel);
  border-top: 1px solid var(--line);
}

.cm-editor-container :deep(.monaco-editor) {
  background-color: var(--panel-alt) !important;
}

.cm-editor-container :deep(.monaco-editor .margin) {
  background-color: var(--panel-alt) !important;
}

.cm-editor-container :deep(.monaco-editor .minimap) {
  border-left: 1px solid var(--line-strong);
}

.cm-editor-container :deep(.monaco-editor .scroll-decoration) {
  box-shadow: none !important;
}

.cm-editor-container :deep(.monaco-editor .view-overlays .current-line) {
  background-color: rgba(59, 130, 246, 0.1) !important;
  border: none !important;
}

.cm-editor-container :deep(.monaco-editor .cursors-layer .cursor) {
  background-color: var(--blue) !important;
  border-color: var(--blue) !important;
}

.cm-editor-container :deep(.monaco-editor .scrollbar .slider) {
  background: rgba(var(--tint-rgb), 0.15) !important;
  border-radius: 4px;
}

.cm-editor-container :deep(.monaco-editor .scrollbar .slider:hover) {
  background: rgba(var(--tint-rgb), 0.25) !important;
}

.cm-editor-container :deep(.monaco-editor .view-lines) {
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', monospace !important;
}
</style>
