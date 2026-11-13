<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div ref="el" class="md"></div>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import 'monaco-editor'
import * as monaco from 'monaco-editor/editor/editor.api'
import editorWorker from 'monaco-editor/editor/editor.worker?worker'

if (typeof window !== 'undefined' && !(window as any).MonacoEnvironment) {
  (window as any).MonacoEnvironment = { getWorker: () => new editorWorker() }
}

const CODICON_STYLE_ID = 'kubron-codicon-font'

// original — левая сторона (всегда только чтение); modified — правая.
// editable=true: правую сторону можно править, изменения уходят в update:modified.
const props = defineProps<{ original: string; modified: string; language?: string; editable?: boolean }>()
const emit = defineEmits<{ (e: 'update:modified', v: string): void }>()

const el = ref<HTMLDivElement | null>(null)
let editor: monaco.editor.IStandaloneDiffEditor | null = null
let origModel: monaco.editor.ITextModel | null = null
let modModel: monaco.editor.ITextModel | null = null
let sub: monaco.IDisposable | null = null

onMounted(() => {
  if (!document.getElementById(CODICON_STYLE_ID)) {
    const style = document.createElement('style')
    style.id = CODICON_STYLE_ID
    style.textContent = `@font-face { font-family: 'codicon'; src: url('${new URL('monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.ttf', import.meta.url).href}') format('truetype'); }`
    document.head.appendChild(style)
  }
  const lang = props.language ?? 'yaml'
  origModel = monaco.editor.createModel(props.original, lang)
  modModel = monaco.editor.createModel(props.modified, lang)
  editor = monaco.editor.createDiffEditor(el.value!, {
    theme: 'vs-dark',
    automaticLayout: true,
    renderSideBySide: true,
    originalEditable: false,
    readOnly: !props.editable,
    domReadOnly: !props.editable,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    fontSize: 12,
    fontFamily: "'Monaco', 'Menlo', monospace",
  })
  editor.setModel({ original: origModel, modified: modModel })
  sub = modModel.onDidChangeContent(() => emit('update:modified', modModel!.getValue()))
})

watch(() => props.original, v => { if (origModel && origModel.getValue() !== v) origModel.setValue(v) })
watch(() => props.modified, v => { if (modModel && modModel.getValue() !== v) modModel.setValue(v) })

onBeforeUnmount(() => {
  sub?.dispose()
  editor?.dispose()
  origModel?.dispose()
  modModel?.dispose()
  editor = origModel = modModel = sub = null
})
</script>

<style scoped>
.md { height: 55vh; min-height: 280px; border: 1px solid var(--line); border-radius: 4px; overflow: hidden; text-align: left; }
.md :deep(.monaco-editor),
.md :deep(.monaco-editor .margin) { background-color: var(--panel-alt) !important; }
</style>
