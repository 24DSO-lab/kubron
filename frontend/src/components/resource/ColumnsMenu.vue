<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div ref="root" class="cm">
    <button
      ref="btn"
      class="cm-btn"
      type="button"
      :title="$t('resource.columns.title')"
      :aria-label="$t('resource.columns.settings')"
      aria-haspopup="true"
      :aria-expanded="open"
      :class="{ active: open }"
      @click.stop="toggle"
    >
      <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <rect x="1.75" y="2.75" width="12.5" height="10.5" rx="1.5" />
        <path d="M6 2.75v10.5M10.25 2.75v10.5" />
      </svg>
    </button>
    <div v-if="open" class="cm-pop" :style="popStyle" role="dialog" :aria-label="$t('resource.columns.settings')" @click.stop>
      <ul class="cm-list">
        <li
          v-for="(c, i) in ordered"
          :key="c.key"
          class="cm-row"
          :class="{ over: overIdx === i }"
          draggable="true"
          @dragstart="onDragStart(i, $event)"
          @dragover.prevent="overIdx = i"
          @dragleave="overIdx = null"
          @drop.prevent="onDrop(i)"
          @dragend="dragFrom = overIdx = null"
        >
          <label class="cm-label">
            <input
              type="checkbox"
              :checked="!hidden.includes(c.key)"
              :disabled="c.key === required"
              @change="emit('toggle', c.key)"
            />
            <span>{{ tl(c.label) }}</span>
          </label>
          <span class="cm-move">
            <button type="button" :disabled="i === 0" :aria-label="$t('resource.columns.moveUp')" @click="emit('move', i, i - 1)">↑</button>
            <button type="button" :disabled="i === ordered.length - 1" :aria-label="$t('resource.columns.moveDown')" @click="emit('move', i, i + 1)">↓</button>
          </span>
        </li>
      </ul>
      <button class="cm-reset" type="button" @click="emit('reset')">{{ $t('resource.columns.reset') }}</button>
    </div>
  </div>
</template>

<script lang="ts" setup>
/** Выпадающее меню: видимость колонок (чекбокс) и порядок (перетаскивание или кнопки ↑/↓). Состояния не хранит. */
import { onMounted, onUnmounted, ref } from 'vue'
import { tl } from '../../i18n'

defineProps<{
  ordered: { key: string; label: string }[]
  hidden: string[]
  /** Ключ колонки, которую нельзя скрыть (имя) */
  required: string
}>()

const emit = defineEmits<{
  (e: 'toggle', key: string): void
  (e: 'move', from: number, to: number): void
  (e: 'reset'): void
}>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const btn = ref<HTMLButtonElement | null>(null)
const popStyle = ref<Record<string, string>>({})
const dragFrom = ref<number | null>(null)
const overIdx = ref<number | null>(null)

function onDragStart(i: number, e: DragEvent) {
  dragFrom.value = i
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(i)) // Firefox/WebKit не начинают drag без данных
  }
}

function onDrop(to: number) {
  if (dragFrom.value !== null && dragFrom.value !== to) emit('move', dragFrom.value, to)
  dragFrom.value = overIdx.value = null
}

const POP_WIDTH = 240

/**
 * Меню позиционируется через fixed: кнопка стоит в th, а у ячеек таблицы overflow: hidden,
 * обычное absolute-меню было бы обрезано.
 */
function toggle() {
  if (open.value) { open.value = false; return }
  const r = btn.value!.getBoundingClientRect()
  const left = Math.max(8, Math.min(r.right - POP_WIDTH, window.innerWidth - POP_WIDTH - 8))
  popStyle.value = { top: `${r.bottom + 6}px`, left: `${left}px`, maxHeight: `${Math.max(160, window.innerHeight - r.bottom - 20)}px` }
  open.value = true
}

const close = () => { open.value = false }

const onDocClick = (e: MouseEvent) => {
  if (!root.value?.contains(e.target as Node)) open.value = false
}
const onKey = (e: KeyboardEvent) => {
  if (e.key === 'Escape') open.value = false
}
onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onKey)
  window.addEventListener('resize', close)
  window.addEventListener('scroll', close, true) // меню fixed: при прокрутке уехало бы от кнопки
})
onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', close)
  window.removeEventListener('scroll', close, true)
})
</script>

<style scoped>
.cm { display: inline-flex; }
.cm-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 0;
  border-radius: 4px;
  color: var(--text-muted);
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}
.cm-btn:hover, .cm-btn.active { background: rgba(var(--tint-rgb), 0.1); color: var(--accent); }
.cm-pop {
  position: fixed;
  z-index: 10000;
  width: 240px;
  overflow: auto;
  background: var(--panel-raised);
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  padding: 6px;
  text-align: left;
  font-weight: 400;
  text-transform: none;
  letter-spacing: normal;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
}
.cm-list { list-style: none; margin: 0; padding: 0; }
.cm-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 4px 6px;
  border-radius: 4px;
  cursor: grab;
}
.cm-row:hover { background: rgba(var(--tint-rgb), 0.06); }
.cm-row.over { outline: 1px dashed var(--accent); }
.cm-label { display: flex; align-items: center; gap: 8px; flex: 1; font-size: 13px; color: var(--text-code); }
.cm-label input { accent-color: var(--accent); margin: 0; }
.cm-move button {
  width: 22px;
  height: 22px;
  margin-left: 2px;
  background: transparent;
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  color: var(--text-code);
  cursor: pointer;
}
.cm-move button:hover:not(:disabled) { border-color: var(--accent); }
.cm-move button:disabled { opacity: 0.3; cursor: default; }
.cm-reset {
  width: 100%;
  margin-top: 6px;
  padding: 6px;
  background: transparent;
  border: 0;
  border-top: 1px solid var(--line-strong);
  color: var(--accent);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}
</style>
