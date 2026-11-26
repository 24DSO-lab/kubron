<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div ref="el" class="me"></div>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import 'monaco-editor'
import { useMonacoTheme } from '../../composables/useMonacoTheme'
import * as monaco from 'monaco-editor/editor/editor.api'
import editorWorker from 'monaco-editor/editor/editor.worker?worker'

if (typeof window !== 'undefined' && !(window as any).MonacoEnvironment) {
  (window as any).MonacoEnvironment = { getWorker: () => new editorWorker() }
}

const CODICON_STYLE_ID = 'kubron-codicon-font'

const monacoTheme = useMonacoTheme(n => monaco.editor.setTheme(n))
const props = defineProps<{ modelValue: string; language?: string; readonly?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

const el = ref<HTMLDivElement | null>(null)
let editor: monaco.editor.IStandaloneCodeEditor | null = null
let sub: monaco.IDisposable | null = null

onMounted(() => {
  if (!document.getElementById(CODICON_STYLE_ID)) {
    const style = document.createElement('style')
    style.id = CODICON_STYLE_ID
    style.textContent = `@font-face { font-family: 'codicon'; src: url('${new URL('monaco-editor/esm/vs/base/browser/ui/codicons/codicon/codicon.ttf', import.meta.url).href}') format('truetype'); }`
    document.head.appendChild(style)
  }
  editor = monaco.editor.create(el.value!, {
    value: props.modelValue,
    language: props.language ?? 'yaml',
    theme: monacoTheme(),
    readOnly: !!props.readonly,
    automaticLayout: true,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    fontSize: 12,
    fontFamily: "'Monaco', 'Menlo', monospace",
    tabSize: 2,
    insertSpaces: true,
  })
  sub = editor.onDidChangeModelContent(() => emit('update:modelValue', editor!.getValue()))
})

watch(() => props.modelValue, v => { if (editor && editor.getValue() !== v) editor.setValue(v) })
watch(() => props.readonly, v => editor?.updateOptions({ readOnly: !!v }))

onBeforeUnmount(() => {
  sub?.dispose()
  editor?.dispose()
  editor = sub = null
})
</script>

<style scoped>
.me { width: 100%; height: 100%; min-height: 200px; border: 1px solid var(--line); border-radius: 4px; overflow: hidden; text-align: left; }
.me :deep(.monaco-editor),
.me :deep(.monaco-editor .margin) { background-color: var(--panel-alt) !important; }
</style>
