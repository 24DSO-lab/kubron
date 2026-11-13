<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
<div class="logs-view">
  <div class="logs-toolbar">
    <div class="toolbar-group">
      <label>{{ $t('utils.logs.pods') }}:</label>
      <select :value="tab.selectedPodName" @change="onPodChange($event)"
        class="select-pod">
        <option value="">{{ $t('utils.logs.allPods') }}</option>
        <option v-for="pod in tab.pods" :key="pod.name" :value="pod.name">{{ pod.name }}</option>
      </select>
    </div>
    <div class="toolbar-group">
      <label>{{ $t('utils.logs.containers') }}:</label>
      <select :value="tab.selectedContainer" @change="onContainerChange($event)"
        class="select-container">
        <option value="">{{ $t('utils.logs.allContainers') }}</option>
        <option v-for="container in availableContainers" :key="container" :value="container">
          {{ container }}</option>
      </select>
    </div>
    <div class="toolbar-group">
      <label>{{ $t('utils.logs.lines') }}:</label>
      <input v-model.number="logLines" type="number" min="10" max="10000" @change="loadLogs()"
        class="input-lines" />
    </div>
    <div class="toolbar-group search-group">
      <input v-model="searchText" type="text" :placeholder="$t('utils.logs.searchPlaceholder')" class="input-search" />
    </div>
    <div class="toolbar-actions">
      <button @click="showTimestamp = !showTimestamp" class="btn-action" :class="{ active: showTimestamp }"><img
          :src="timestampIcon" class="icon-svg" /></button>
      <button @click="wrapLines = !wrapLines" class="btn-action" :class="{ active: wrapLines }"><img
          :src="wrapLinesIcon" class="icon-svg" /></button>
      <button @click="loadLogs()" class="btn-action btn-refresh"><img :src="restartIcon"
          class="icon-svg" /></button>
      <button @click="downloadLogs()" class="btn-action"><img :src="downloadIcon"
          class="icon-svg" /></button>
    </div>
  </div>
<div class="logs-info"><span>{{ $t('common.namespace') }}: <strong>{{ tab.namespace }}</strong> | {{ tab.resourceType }}:
    <strong>{{ tab.resourceName }}</strong> | {{ $t('utils.logs.pods') }}: <strong>{{ tab.pods.length }}</strong></span>
  <span v-if="live" class="live-dot">● {{ $t('utils.logs.live') }}</span>
  <button v-if="!follow" class="btn-action" @click="jumpToEnd">↓ {{ $t('utils.logs.toEnd') }}</button>
</div>
<div ref="contentEl" class="logs-content" @scroll="onScroll">
  <div v-if="tab.loading" class="logs-loading">⏳ {{ $t('common.loading') }}</div>
  <pre v-else-if="tab.rawLogs" class="logs-text" :class="{ 'no-wrap': !wrapLines }" v-html="highlightedLogs"></pre>
  <div v-else class="logs-empty">{{ live ? $t('utils.logs.waiting') : tab.pods.length ? $t('utils.logs.noLogs') : $t('utils.logs.noPods') }}</div>
</div>
</div>
</template>

<script lang="ts" setup>

import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import { getWorkloadPods } from '../../resources/workloadPods'
import { containersOf } from '../../resources/utils'
import type { LogsTab } from '../../composables/useTabbedPanel'
import restartIcon from '../../assets/icons/restart.svg'
import downloadIcon from '../../assets/icons/download.svg'
import timestampIcon from '../../assets/icons/timestamp.svg'
import wrapLinesIcon from '../../assets/icons/wrap-lines.svg'
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { onEvent } from '../../platform/events'
import { RECONNECTED } from '../../platform/events.web'
import { newStreamId, retryElsewhere } from '../../platform/streams'
import { callApp } from '../../platform/api'

const props = defineProps<{ tab: LogsTab; active: boolean }>()
const { t } = useI18n()


const TS_RE = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z[+-]?\d{0,2}:?\d{0,2})\s+(.*)$/

// Настройки просмотра относятся к конкретной вкладке
const logLines = ref(100)
const searchText = ref('')
const showTimestamp = ref(false)
const wrapLines = ref(true)

/** Контейнер по умолчанию: обычный (не init), иначе первый из списка */
function defaultContainer(pod: any): string {
  return containersOf(pod)[0]?.name || pod?.containers?.[0]?.name || ''
}

const availableContainers = computed<string[]>(() => {
  const { selectedPodName, pods } = props.tab
  if (!selectedPodName) {
    const names = new Set<string>()
    pods.forEach(pod => pod.containers?.forEach(c => names.add(c.name)))
    return Array.from(names).sort()
  }
  return pods.find(p => p.name === selectedPodName)?.containers?.map(c => c.name) ?? []
})

const displayedLogs = computed(() => {
  const raw = props.tab.rawLogs
  if (!raw) return ''
  let lines = raw.split('\n').map(line => {
    if (!line.trim()) return line
    const m = line.match(TS_RE)
    if (!m) return line
    return showTimestamp.value ? `[${m[1]}] ${m[2]}` : m[2]
  })
  if (searchText.value) {
    const search = searchText.value.toLowerCase()
    lines = lines.filter(line => line.toLowerCase().includes(search))
  }
  return lines.join('\n')
})

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

// HTML для v-html: вывод контейнера недоверенный, поэтому всегда экранируется;
// искомая строка экранируется так же, чтобы совпадать с экранированным текстом.
const highlightedLogs = computed(() => {
  const logs = escapeHtml(displayedLogs.value)
  if (!searchText.value) return logs
  const term = escapeHtml(searchText.value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return logs.replace(new RegExp(`(${term})`, 'gi'), '<mark class="search-highlight">$1</mark>')
})

async function loadResourcePods() {
  const tab = props.tab
  try {
    tab.loading = true
    // Поды ресурса определяются по владельцу (см. resources/workloadPods.ts)
    tab.pods = await getWorkloadPods(tab.resourceType, tab.namespace, tab.resourceName)
    if (tab.pods.length > 0 && !tab.selectedPodName) {
      tab.selectedPodName = tab.pods[0].name
      tab.selectedContainer = defaultContainer(tab.pods[0])
    }
    if (tab.selectedPodName || tab.pods.length > 0) await loadLogs()
  } catch (e: any) { tab.rawLogs = t('utils.logs.error', { message: errorText(e) }) }
  finally { tab.loading = false }
}

async function onPodChange(event: Event) {
  const tab = props.tab
  tab.selectedPodName = (event.target as HTMLSelectElement).value
  tab.selectedContainer = ''
  if (tab.selectedPodName) {
    const pod = tab.pods.find(p => p.name === tab.selectedPodName)
    if (pod) tab.selectedContainer = defaultContainer(pod)
  }
  await loadLogs()
}

async function onContainerChange(event: Event) {
  props.tab.selectedContainer = (event.target as HTMLSelectElement).value
  await loadLogs()
}


function downloadLogs() {
  const tab = props.tab
  const element = document.createElement('a')
  element.setAttribute('href', 'data:text/plain;charset=utf-8,' + encodeURIComponent(displayedLogs.value))
  element.setAttribute('download', `${tab.resourceType}-${tab.resourceName}-logs.txt`)
  element.style.display = 'none'
  document.body.appendChild(element)
  element.click()
  document.body.removeChild(element)
}



const MAX_LOG_CHARS = 2_000_000
const TS_PREFIXED = /^(\d{4}-\d{2}-\d{2}T\S+)\s(.*)$/gm

interface LiveStream { id: string; unsubscribe: Array<() => void> }
let streams: LiveStream[] = []
let generation = 0
let started = false

const live = ref(false)
const follow = ref(true)
const contentEl = ref<HTMLElement | null>(null)

function stopStream(s: LiveStream) {
  s.unsubscribe.forEach(off => off())
  // Поток мог остаться на другой реплике: запрос повторяется, пока не попадёт к владельцу (ошибки не важны)
  retryElsewhere(() => callApp('StopLogStream', s.id), 4).catch(() => { })
}
function stopStreams() {
  generation++
  streams.forEach(stopStream)
  streams = []
  live.value = false
}
function dropStream(s: LiveStream) {
  s.unsubscribe.forEach(off => off())
  streams = streams.filter(x => x !== s)
  live.value = streams.length > 0
}

function append(text: string) {
  let next = (props.tab.rawLogs ?? '') + text
  if (next.length > MAX_LOG_CHARS) {
    // ограничиваем буфер, режем по границе строки
    const from = next.length - MAX_LOG_CHARS
    const cut = next.indexOf('\n', from)
    next = next.slice(cut < 0 ? from : cut + 1)
  }
  props.tab.rawLogs = next
}

/** Несколько подов: имя пода вставляем после timestamp, чтобы не ломать его разбор (TS_RE) */
const tagPod = (pod: string, chunk: string) => chunk.replace(TS_PREFIXED, `$1 [${pod}] $2`)

/** (Пере)запуск потоков: один под — один поток */
async function loadLogs(resume = false) {
  const tab = props.tab
  stopStreams()
  const gen = generation
  if (resume) { // потоки потеряны вместе с репликой: подписка заново с текущего момента, прежний текст остаётся
    append(`\n${t('utils.logs.streamRestored')}\n`)
  } else {
    tab.rawLogs = ''
    follow.value = true
  }
  const targets = tab.selectedPodName ? tab.pods.filter(p => p.name === tab.selectedPodName) : tab.pods
  if (!targets.length) return
  const multi = !tab.selectedPodName && targets.length > 1

  for (const pod of targets) {
    const container = tab.selectedContainer || defaultContainer(pod)
    const id = await newStreamId('log')
    if (gen !== generation) return // пока ждали готовности сокета событий, запустили новую загрузку
    const s: LiveStream = { id, unsubscribe: [] }
    s.unsubscribe.push(
      onEvent(`logs:data:${id}`, (chunk: string) => append(multi ? tagPod(pod.name, chunk) : chunk)),
      onEvent(`logs:end:${id}`, (reason: string) => {
        const why = reason && reason !== 'eof' ? `: ${reason}` : ''
        append(`\n${t('utils.logs.streamClosed', { pod: multi ? pod.name + ' ' : '', reason: why })}\n`)
        dropStream(s)
      }),
    )
    streams.push(s)
    try {
      await retryElsewhere(() => callApp('StartLogStream', id, tab.namespace, pod.name, container, resume ? 0 : logLines.value))
    } catch (e: any) {
      if (gen === generation) append(`\n[${pod.name}: ${errorText(e)}]\n`)
      dropStream(s)
    }
    if (gen !== generation) { stopStream(s); return } // пока ждали, запустили новую загрузку
  }
  live.value = streams.length > 0
}

function onScroll() {
  const el = contentEl.value
  if (el) follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}
function jumpToEnd() {
  follow.value = true
  const el = contentEl.value
  if (el) el.scrollTop = el.scrollHeight
}
watch(() => props.tab.rawLogs, async () => {
  if (!follow.value) return
  await nextTick()
  jumpToEnd()
})

// Первая загрузка — когда вкладка впервые становится активной; потоки живут, пока вкладка открыта
watch(() => props.active, active => {
  if (active && !started) { started = true; loadResourcePods() }
}, { immediate: true })

// Сокет событий восстановлен (например, реплика с потоками остановилась): открытые потоки больше ничего не
// присылают, открываем их заново на живой реплике.
const offReconnected = onEvent(RECONNECTED, () => { if (streams.length > 0) loadLogs(true) })
onBeforeUnmount(() => { offReconnected(); stopStreams() })



</script>

<style scoped>
/* Logs View */
.logs-view {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.logs-toolbar {
  display: flex;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--line-strong);
  background: #232430;
  flex-wrap: wrap;
  align-items: center;
}

.toolbar-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.toolbar-group label {
  color: #999;
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
}

.select-pod,
.select-container,
.input-lines,
.input-search {
  background: var(--panel-alt);
  border: 1px solid var(--line-strong);
  color: var(--text-code);
  padding: 6px 8px;
  border-radius: 3px;
  font-size: 12px;
}

.input-lines {
  width: 60px;
}

.search-group {
  flex: 1;
  min-width: 200px;
}

.input-search {
  width: 100%;
  padding: 6px 10px;
}

.toolbar-actions {
  display: flex;
  gap: 6px;
}

.btn-action {
  background: none;
  border: 1px solid var(--line-strong);
  color: #999;
  cursor: pointer;
  padding: 6px 10px;
  border-radius: 3px;
  font-size: 12px;
  transition: all 0.2s;
}

.btn-action:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.btn-action.active {
  background: rgba(79, 99, 255, 0.2);
  border-color: #4f63ff;
  color: #4f63ff;
}

.btn-refresh {
  padding: 6px 8px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.icon-svg {
  width: 14px;
  height: 14px;
  stroke-width: 2;
  filter: invert(1);
}

.logs-info {
  padding: 8px 16px;
  background: #232430;
  border-bottom: 1px solid var(--line-strong);
  font-size: 12px;
  color: var(--text-code);
}

.logs-content {
  flex: 1;
  overflow: auto;
  background: var(--panel-alt);
}

.logs-text {
  margin: 0;
  padding: 16px;
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-code);
  white-space: pre-wrap;
  word-wrap: break-word;
  text-align: left;
}

.logs-text.no-wrap {
  white-space: pre;
  word-wrap: normal;
  word-break: normal;
  overflow-x: auto;
}

.logs-loading,
.logs-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #666;
  font-size: 14px;
}

.search-highlight {
  background-color: #7a5e00;
  color: #ffd700;
  font-weight: bold;
}
.live-dot { margin-left: 12px; color: var(--ok); }
</style>
