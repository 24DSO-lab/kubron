<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div ref="el" class="mr"></div>
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

const props = defineProps<{ value: string; language?: string }>()
const el = ref<HTMLDivElement | null>(null)
let editor: monaco.editor.IStandaloneCodeEditor | null = null

onMounted(() => {
  if (!document.getElementById(CODICON_STYLE_ID)) {
    const style = document.createElement('style')
    style.id = CODICON_STYLE_ID
    style.textContent = `@font-face { font-family: 'codicon'; src: url('${new URL('monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.ttf', import.meta.url).href}') format('truetype'); }`
    document.head.appendChild(style)
  }
  editor = monaco.editor.create(el.value!, {
    value: props.value,
    language: props.language ?? 'yaml',
    theme: 'vs-dark',
    readOnly: true,
    domReadOnly: true,
    automaticLayout: true,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    fontSize: 12,
    fontFamily: "'Monaco', 'Menlo', monospace",
    folding: true,
  })
})

watch(() => props.value, v => { if (editor && editor.getValue() !== v) editor.setValue(v) })
watch(() => props.language, l => {
  const model = editor?.getModel()
  if (model && l) monaco.editor.setModelLanguage(model, l)
})
onBeforeUnmount(() => { editor?.dispose(); editor = null })
</script>

<style scoped>
.mr { height: 55vh; min-height: 280px; border: 1px solid var(--line); border-radius: 4px; overflow: hidden; text-align: left; }
.mr :deep(.monaco-editor),
.mr :deep(.monaco-editor .margin) { background-color: var(--panel-alt) !important; }
</style>