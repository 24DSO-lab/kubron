<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
<div class="terminal-view">
  <div class="terminal-header">
    <div class="terminal-title"><span>{{ tab.mode === 'shell' ? $t('utils.terminal.shell') : $t('utils.terminal.attach') }}:
        <strong>{{ tab.container }}</strong> @ {{ tab.pod }}</span></div>
    <div class="terminal-controls">
      <select :value="tab.container"
        @change="onContainerChange($event)" class="container-select">
        <option v-for="c in containerNames" :key="c" :value="c">{{ c }}</option>
      </select>
      <span class="status-indicator">{{ status }}</span>
      <button class="btn btn-sm" @click="reconnect()"
        :disabled="connecting">🔄</button>
    </div>
  </div>
  <div ref="termEl" class="terminal-container"></div>
</div>
</template>

<script lang="ts" setup>
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'
import 'xterm/css/xterm.css'
import type { TerminalTab } from '../../composables/useTabbedPanel'
import { callApp } from '../../platform/api'
import { openTerminal, type TerminalHandle } from '../../platform/terminal'

const props = defineProps<{ tab: TerminalTab; active: boolean }>()
const { t } = useI18n()

const DEFAULT_COLS = 80
const DEFAULT_ROWS = 24

const termEl = ref<HTMLDivElement | null>(null)
const status = ref('🟡')
/** true, пока идёт подключение: кнопка переподключения недоступна */
const connecting = ref(true)

interface Session {
  terminal: Terminal
  fitAddon: FitAddon
  handle: TerminalHandle
  aborted: boolean
  resizeObserver?: ResizeObserver
  resizeTimer?: ReturnType<typeof setTimeout>
  onMouse?: () => void
}

let session: Session | null = null
let unmounted = false

const containerNames = computed<string[]>(() => {
  const tab = props.tab
  if (tab.containers && tab.containers.length > 0) return tab.containers.map(c => c.name)
  return tab.podObj?.containers?.map((c: any) => c.name) || []
})

function setState(s: string, isConnecting: boolean) {
  status.value = s
  connecting.value = isConnecting
}

function safeFit() {
  try { session?.fitAddon.fit() } catch { /* контейнер может быть скрыт */ }
}

function debouncedResize(s: Session, cols: number, rows: number) {
  if (s.resizeTimer) clearTimeout(s.resizeTimer)
  s.resizeTimer = setTimeout(() => { s.handle.resize(cols, rows) }, 150)
}

// async function readLoop(s: Session) {
//   while (!s.aborted) {
//     try {
//       const result: any = await ReadTerminal(s.sessionID)
//       if (s.aborted) break
//       if (!result) {
//         s.terminal.write('\r\n[Session ended]\r\n')
//         setState('🔴', false)
//         break
//       }
//       if (typeof result === 'string' && result.length > 0) {
//         try { s.terminal.write(atob(result)) } catch { s.terminal.write(result) }
//       }
//     } catch {
//       if (!s.aborted) {
//         s.terminal.write('\r\n[Connection lost]\r\n')
//         setState('🔴', false)
//       }
//       break
//     }
//   }
// }



// readLoop удалён: данные приходят событиями terminal:data:<id> / terminal:exit:<id>

function disposeSession() {
  const s = session
  session = null
  if (!s) return
  s.aborted = true
  if (s.resizeTimer) clearTimeout(s.resizeTimer)
  s.resizeObserver?.disconnect()
  if (s.onMouse) termEl.value?.removeEventListener('mousedown', s.onMouse)
  s.handle.close()
  try { s.terminal.dispose() } catch { /* уже освобождён */ }
}

async function initTerminal() {
  const container = termEl.value
  if (!container) return
  const tab = props.tab
  setState('🟡', true)
  try {
    let shell = '/bin/sh'
    try { shell = await callApp('DetectShell', tab.namespace, tab.pod, tab.container) } catch { shell = '/bin/sh' }
    if (unmounted) return

    container.innerHTML = ''
    const terminal = new Terminal({
      fontFamily: 'Monaco, Menlo, "Courier New", monospace',
      fontSize: 13,
      lineHeight: 1.2,
      theme: { background: '#1e1e1e', foreground: '#d4d4d4', cursor: '#00ff00', cursorAccent: '#00ff00' },
      cursorBlink: true,
      disableStdin: false,
      cols: DEFAULT_COLS,
      rows: DEFAULT_ROWS,
      allowProposedApi: true,
      scrollback: 10000,
      tabStopWidth: 4,
    })
    const fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.open(container)

    await nextTick()
    await new Promise(r => setTimeout(r, 150))
    if (unmounted) { terminal.dispose(); return }
    try { fitAddon.fit() } catch { /* скрыт */ }

    const sessionMode = tab.mode === 'attach' ? '' : shell
    // Сессия создаётся без handle: обработчики вывода ссылаются на неё до возврата openTerminal
    const s = { terminal, fitAddon, aborted: false } as Session
    try {
      s.handle = await openTerminal(
        { namespace: tab.namespace, pod: tab.pod, container: tab.container, shell: sessionMode, cols: terminal.cols, rows: terminal.rows },
        {
          onData: bytes => { if (!s.aborted) terminal.write(bytes) },
          onExit: reason => {
            if (s.aborted) return
            terminal.write(reason === 'exit' ? '\r\n[Session ended]\r\n' : '\r\n[Connection lost]\r\n')
            setState('🔴', false)
            if (reason === 'lost') reopenAfterLoss()
          },
        },
      )
    } catch (e) {
      s.aborted = true
      terminal.dispose()
      throw e // обработает общий catch ниже
    }
    if (unmounted) {
      s.aborted = true
      s.handle.close()
      terminal.dispose()
      return
    }
    session = s
    lastOpenedAt = Date.now()
    if (reopenNote) { terminal.write(reopenNote); reopenNote = '' }
    setState('🟢', false)

    terminal.onData((data: string) => s.handle.write(data))
    terminal.onResize(({ cols, rows }) => debouncedResize(s, cols, rows))

    s.resizeObserver = new ResizeObserver(() => { if (props.active) safeFit() })
    s.resizeObserver.observe(container)
    s.onMouse = () => terminal.focus()
    container.addEventListener('mousedown', s.onMouse)

    await new Promise(r => setTimeout(r, 100))
    safeFit()
    if (props.active) terminal.focus()
    if (props.active) terminal.focus()
  } catch (e) {
    console.error('Terminal init error:', e)
    setState('🔴', false)
  }
}

// Потеря соединения в вебе (реплика остановилась): открываем новую оболочку на живой реплике и пишем об этом.
// Процесс прежнего терминала погиб вместе с репликой, поэтому состояние не восстановить. Число подряд идущих
// попыток ограничено, чтобы не зациклиться, если терминал не открывается.
const MAX_AUTO_REOPEN = 3
const REOPEN_HEALTHY_MS = 30_000
let autoReopens = 0
let lastOpenedAt = 0
let reopenNote = ''
function reopenAfterLoss() {
  if (import.meta.env.VITE_TARGET !== 'web' || unmounted) return
  if (Date.now() - lastOpenedAt > REOPEN_HEALTHY_MS) autoReopens = 0
  if (autoReopens >= MAX_AUTO_REOPEN) return
  autoReopens++
  reopenNote = '\r\n[Connection restored: new shell]\r\n'
  setTimeout(() => { if (!unmounted) reconnect() }, 300 + Math.random() * 1000)
}

async function reconnect() {
  if (connecting.value) return
  disposeSession()
  await initTerminal()
}

function onContainerChange(event: Event) {
  props.tab.container = (event.target as HTMLSelectElement).value
  reconnect()
}

// Вкладка снова видна: подогнать размер и вернуть фокус
watch(() => props.active, async active => {
  if (!active) return
  await nextTick()
  safeFit()
  session?.terminal.focus()
})

onMounted(async () => {
  await nextTick()
  await new Promise(r => setTimeout(r, 50))
  await initTerminal()
})

onBeforeUnmount(() => {
  unmounted = true
  disposeSession()
})
</script>

<style scoped>
/* Terminal View */
.terminal-view {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.terminal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 16px;
  border-bottom: 1px solid #3b3d5c;
  background: #252526;
}

.terminal-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: #d4d4d4;
}

.terminal-controls {
  display: flex;
  gap: 8px;
  align-items: center;
}

.container-select {
  background: #1a1b26;
  border: 1px solid #3b3d5c;
  color: #d4d4d4;
  padding: 4px 8px;
  border-radius: 3px;
  font-size: 12px;
  min-width: 100px;
  max-width: 200px;
}

.status-indicator {
  font-size: 12px;
}

.terminal-container {
  flex: 1;
  overflow: hidden;
  padding: 0;
  margin: 0;
  position: relative;
  background: #1e1e1e;
  padding-left: 8px !important;
}

:deep(.xterm) {
  width: 100% !important;
  height: 100% !important;
  padding: 0 !important;
  margin: 0 !important;
  position: relative !important;
  left: 0 !important;
}

:deep(.xterm-cursor-layer) {
  z-index: 10 !important;
}

:deep(.xterm-screen) {
  margin: 0 !important;
  padding: 0 !important;
  position: relative !important;
  left: 20px !important;
  text-align: left !important;
}

:deep(.xterm-rows) {
  padding-left: 0 !important;
  margin-left: 0 !important;
}

:deep(.xterm-viewport) {
  overflow-y: auto !important;
  overflow-x: hidden !important;
  position: absolute !important;
  top: 0 !important;
  right: 0 !important;
  bottom: 0 !important;
  width: 12px !important;
  z-index: 100 !important;
}

:deep(.xterm-scroll-area) {
  padding-left: 0 !important;
  margin-left: 0 !important;
}

:deep(.xterm-char-measure-element) {
  position: absolute !important;
  left: -10000px !important;
}

/* Убираем отступы у строк терминала */
:deep(.xterm .xterm-row) {
  padding-left: 0 !important;
  margin-left: 0 !important;
}

/* Скроллбар для терминала */
:deep(.xterm-viewport::-webkit-scrollbar) {
  width: 10px !important;
}

:deep(.xterm-viewport::-webkit-scrollbar-track) {
  background: #1e1e1e !important;
}

:deep(.xterm-viewport::-webkit-scrollbar-thumb) {
  background: #444 !important;
  border-radius: 5px !important;
}

:deep(.xterm-viewport::-webkit-scrollbar-thumb:hover) {
  background: #555 !important;
}

.btn {
  padding: 6px 12px;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
}

.btn-sm {
  padding: 4px 8px;
}

.btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
