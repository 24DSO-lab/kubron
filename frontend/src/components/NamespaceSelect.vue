<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div ref="root" class="ns-multi">
    <button
      ref="trigger"
      type="button"
      class="ns-trigger"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-label="`Namespace: ${summary}`"
      :title="draft.join(', ')"
      @click="toggleOpen"
      @keydown.down.prevent="openAndFocus"
    >
      <span class="ns-trigger-text">{{ summary }}</span>
      <span class="ns-chevron" aria-hidden="true">▴</span>
    </button>

    <!-- Список открывается вверх: селектор стоит в самом низу бокового меню -->
    <div v-if="open" class="ns-popup" @keydown="onKeydown">
      <input
        ref="searchEl"
        v-model="query"
        class="ns-search"
        type="text"
        :placeholder="$t('helm.namespace_select.searchPlaceholder')"
        :aria-label="$t('helm.namespace_select.searchAria')"
        role="combobox"
        aria-expanded="true"
        :aria-controls="listId"
        :aria-activedescendant="activeId"
        autocomplete="off"
        spellcheck="false"
      />

      <ul :id="listId" class="ns-list" role="listbox" aria-multiselectable="true" :aria-label="$t('helm.namespace_select.label')">
        <li
          v-for="(row, i) in rows"
          :id="optionId(i)"
          :key="row.key"
          role="option"
          :aria-selected="row.selected"
          :class="['ns-option', { active: i === activeIndex, selected: row.selected }]"
          @click="activate(row)"
          @mousemove="activeIndex = i"
        >
          <span class="ns-check" aria-hidden="true">{{ row.selected ? '☑' : '☐' }}</span>
          <span class="ns-name">{{ row.label }}</span>
          <span
            v-if="row.error"
            class="ns-noaccess"
            :title="row.error"
          >{{ $t('helm.namespace_select.noAccess') }}</span>
        </li>
        <li v-if="!rows.length" class="ns-empty" role="presentation">{{ $t('helm.namespace_select.noNamespaces') }}</li>
      </ul>

      <div class="ns-footer">
        <button type="button" class="ns-link" :disabled="!filtered.length || atLimit" @click="selectShown">{{ $t('helm.namespace_select.selectShown') }}</button>
        <button type="button" class="ns-link" :disabled="!draft.length" @click="clear">{{ $t('helm.namespace_select.clear') }}</button>
        <span v-if="atLimit" class="ns-limit">max {{ MAX_NAMESPACES }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { NamespaceInfo } from '../types'
import {
  MAX_NAMESPACES, normalizeSelection, sameSelection, summarizeSelection, toggleNamespace,
} from '../composables/namespaceSelection'

const props = defineProps<{
  modelValue: string[]
  namespaces: NamespaceInfo[]
  /** namespace -> текст ошибки: отказ, полученный при чтении списков */
  noAccess?: Record<string, string>
}>()

const emit = defineEmits<{ (e: 'update:modelValue', value: string[]): void }>()

interface Row { key: string; label: string; ns: string | null; selected: boolean; error?: string }

const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)
const searchEl = ref<HTMLInputElement | null>(null)
const open = ref(false)
const query = ref('')
const activeIndex = ref(0)
/** Текущий выбор — всегда значение родителя: изменения уходят наверх сразу, без черновика */
const draft = computed(() => props.modelValue)
const listId = `ns-list-${Math.random().toString(36).slice(2, 8)}`

const summary = computed(() => summarizeSelection(draft.value))
const atLimit = computed(() => draft.value.length >= MAX_NAMESPACES)

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return props.namespaces.filter(n => !q || n.name.toLowerCase().includes(q))
})

/** Первая строка — «All namespaces» (пустой выбор); при поиске её нет */
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  if (!query.value.trim()) {
    out.push({ key: '*', label: 'All namespaces', ns: null, selected: draft.value.length === 0 })
  }
  for (const n of filtered.value) {
    out.push({
      key: n.name, label: n.name, ns: n.name,
      selected: draft.value.includes(n.name),
      error: props.noAccess?.[n.name],
    })
  }
  return out
})

const optionId = (i: number) => `${listId}-opt-${i}`
const activeId = computed(() => (rows.value.length ? optionId(activeIndex.value) : undefined))

watch(query, () => { activeIndex.value = 0 })
watch(rows, r => { if (activeIndex.value >= r.length) activeIndex.value = Math.max(0, r.length - 1) })

/** Выбор применяется в момент клика; нормализация и сохранение — на стороне родителя */
function setDraft(next: string[]) {
  const normalized = normalizeSelection(next)
  if (!sameSelection(normalized, draft.value)) emit('update:modelValue', normalized)
}

function activate(row: Row) {
  if (row.ns === null) setDraft([])
  else setDraft(toggleNamespace(draft.value, row.ns))
}

function selectShown() {
  setDraft(normalizeSelection([...draft.value, ...filtered.value.map(n => n.name)]))
}

function clear() {
  setDraft([])
}

async function openAndFocus() {
  if (!open.value) await toggleOpen()
}

async function toggleOpen() {
  if (open.value) { close(); return }
  open.value = true
  query.value = ''
  activeIndex.value = 0
  document.addEventListener('pointerdown', onOutside, true)
  await nextTick()
  searchEl.value?.focus()
}

function close(returnFocus = true) {
  if (!open.value) return
  open.value = false
  document.removeEventListener('pointerdown', onOutside, true)
  if (returnFocus) trigger.value?.focus()
}

function onOutside(e: Event) {
  if (root.value && !root.value.contains(e.target as Node)) close(false)
}

function onKeydown(e: KeyboardEvent) {
  const last = rows.value.length - 1
  switch (e.key) {
    case 'ArrowDown': activeIndex.value = Math.min(last, activeIndex.value + 1); break
    case 'ArrowUp': activeIndex.value = Math.max(0, activeIndex.value - 1); break
    case 'Home': if (e.target === searchEl.value && query.value) return; activeIndex.value = 0; break
    case 'End': if (e.target === searchEl.value && query.value) return; activeIndex.value = Math.max(0, last); break
    case 'Enter': {
      const row = rows.value[activeIndex.value]
      if (row) activate(row)
      break
    }
    case ' ': {
      // пробел в поле поиска остаётся символом, пока пользователь что-то вводит
      if (e.target === searchEl.value && query.value) return
      const row = rows.value[activeIndex.value]
      if (row) activate(row)
      break
    }
    case 'Escape': close(); break
    case 'Tab': close(false); return
    default: return
  }
  e.preventDefault()
  nextTick(() => document.getElementById(optionId(activeIndex.value))?.scrollIntoView?.({ block: 'nearest' }))
}

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutside, true)
})
</script>

<style scoped>
.ns-multi { position: relative; }
.ns-trigger {
  width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 6px;
  padding: 6px 8px; background: var(--surface-0); color: var(--text); border: 1px solid var(--surface-1);
  border-radius: 4px; font-size: 12px; cursor: pointer; text-align: left;
}
.ns-trigger:focus-visible, .ns-trigger[aria-expanded='true'] { outline: none; border-color: var(--accent); }
.ns-trigger-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ns-chevron { color: var(--text-muted); font-size: 10px; flex-shrink: 0; }

.ns-popup {
  position: absolute; left: 0; right: 0; bottom: calc(100% + 4px); z-index: 50;
  min-width: 200px; background: var(--bg); border: 1px solid var(--surface-1); border-radius: 6px;
  box-shadow: 0 -6px 20px rgba(0, 0, 0, 0.45); display: flex; flex-direction: column;
}
.ns-search {
  margin: 6px; padding: 5px 8px; background: var(--surface-0); color: var(--text);
  border: 1px solid var(--surface-1); border-radius: 4px; font-size: 12px;
}
.ns-search:focus { outline: none; border-color: var(--accent); }
.ns-list { list-style: none; margin: 0; padding: 0 4px; max-height: 260px; overflow-y: auto; }
.ns-option {
  display: flex; align-items: center; justify-content: flex-start; gap: 6px; padding: 5px 8px; border-radius: 4px;
  font-size: 12px; color: var(--text); cursor: pointer; user-select: none; text-align: left;
}
.ns-option.active { background: rgba(var(--accent-rgb), 0.12); }
.ns-option.selected .ns-check { color: var(--accent); }
.ns-check { width: 14px; flex-shrink: 0; color: var(--text-muted); }
.ns-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ns-noaccess {
  flex-shrink: 0; font-size: 10px; color: var(--warn); border: 1px solid #f9e2af55;
  border-radius: 3px; padding: 0 4px;
}
.ns-empty { padding: 8px; font-size: 12px; color: var(--text-subtle); }
.ns-footer {
  display: flex; align-items: center; gap: 12px; padding: 6px 10px;
  border-top: 1px solid var(--surface-0);
}
.ns-link { background: none; border: none; padding: 0; color: var(--accent); font-size: 11px; cursor: pointer; }
.ns-link:disabled { color: var(--surface-2); cursor: default; }
.ns-limit { margin-left: auto; font-size: 10px; color: var(--warn); }
</style>
