<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div>
    <div v-if="menu" class="menu-overlay" @click="hideMenu" @contextmenu.prevent="hideMenu"></div>
    <ContentHeader :icon="icon" :title="title" :count="items.length" :search="search" :refreshing="refreshing" :updated-at="updatedAt" showSearch @refresh="emit('refresh')" @search="v => emit('search', v)" />

    <table
  v-if="items.length > 0"
  ref="tableRef"
  class="table"
  :class="{ 'table-fixed': fixed }"
  :style="fixed ? { width: totalWidth + 'px' } : undefined"
>
  <colgroup v-if="fixed">
  <col v-if="selectable" :style="{ width: SELECT_COL + 'px' }" />
  <col v-for="col in visibleColumns" :key="col.key" :style="{ width: widths[col.key] + 'px' }" />
  <col :style="{ width: ACTION_COL + 'px' }" />
</colgroup>
<thead>
  <tr>
    <th v-if="selectable" class="col-select" @click.stop>
      <input
        ref="allBox"
        type="checkbox"
        class="row-check"
        :title="$t('resource.table.selectAll')"
        :checked="allSelected"
        @change="emit('toggle-all')"
      />
    </th>
    <th v-for="col in visibleColumns" :key="col.key" :data-col="col.key" :aria-sort="ariaSort(col)">
      <button v-if="isSortable(col)" type="button" class="th-sort" @click="onSort(col)">
        {{ tl(col.label) }}<span class="sort-ind" aria-hidden="true">{{ sortIndicator(col) }}</span>
      </button>
      <template v-else>{{ tl(col.label) }}</template>
      <span
        class="col-resizer"
        :class="{ active: resizingKey === col.key }"
        :title="$t('resource.table.resetWidth')"
        @pointerdown.prevent.stop="startResize(col.key, $event)"
        @pointermove="onDrag"
        @pointerup="stopDrag"
        @pointercancel="stopDrag"
        @dblclick.stop="resetWidths"
      ></span>
    </th>
    <th class="col-actions">
      <ColumnsMenu
        :ordered="resolved.ordered"
        :hidden="prefs.hidden"
        :required="columns[0]?.key ?? ''"
        @toggle="toggleColumn"
        @move="moveColumn"
        @reset="resetColumns"
      />
    </th>
  </tr>
</thead>
      <tbody>
        <tr
          v-for="item in sortedItems"
          :key="itemKey(item)"
          class="clickable-row"
          data-testid="resource-row"
          :data-row-key="itemKey(item)"
          :class="{ 'row-selected': selectable && isSelected(item) }"
          @click="emit('select', item)"
          @contextmenu.prevent="openMenuAt(item, $event)"
        >
          <td v-if="selectable" class="col-select" @click.stop>
            <input type="checkbox" class="row-check" :checked="isSelected(item)" @change="emit('toggle', item)" />
          </td>
          <td v-for="col in visibleColumns" :key="col.key" :class="col.className">
            <component :is="col.component" v-if="col.component" :item="item" :col="col" />
<span v-else-if="col.animated" :style="cellStyle(col, item)">
  <RollingValue :value="String(cellValue(col, item))" />
</span>
            <span v-else-if="col.link" :style="cellStyle(col, item)">
              <template v-if="col.link(item)">
                <a
                  class="rd-link"
                  @click.stop="emit('open', col.link(item)!)"
                >
                  {{ col.linkLabel ? (col.linkLabel(item) ?? cellValue(col, item)) : cellValue(col, item) }}
                </a>
              </template>
              <template v-else>{{ cellValue(col, item) }}</template>
            </span>
            <span v-else :style="cellStyle(col, item)">{{ cellValue(col, item) }}</span>
          </td>
          <td @click.stop>
            <button class="btn-icon" @click.stop="openMenu(item, $event)">⋮</button>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty">{{ emptyMessage }}</div>

    <!-- Как в Lens: «−» удаляет выбранные строки, «+» открывает редактор с базовым манифестом -->
    <div v-if="!hideFab && (selectable || showCreate)" class="fab-group">
      <button
        v-if="selectable"
        type="button"
        class="fab fab-minus"
        :class="{ 'fab-danger': selectedCount > 0 }"
        :disabled="selectedCount === 0"
        :title="selectedCount > 0 ? $t('resource.table.deleteSelected', { n: selectedCount }) : $t('resource.table.selectToDelete')"
        @click="emit('delete-selected')"
      >
        −<span v-if="selectedCount > 0" class="fab-badge">{{ selectedCount }}</span>
      </button>
      <button
        v-if="showCreate"
        type="button"
        class="fab fab-plus"
        :title="createTitle ?? $t('common.create')"
        @click="emit('create')"
      >
        +
      </button>
    </div>

    <!-- Контекстное меню целиком строится из menuFor(item): подменю контейнеров задают сами пункты -->
    <div v-if="menu" :style="menuStyle" class="cluster-context-menu" @click.stop>
      <div
        v-for="(entry, i) in menu.entries"
        :key="entry.action"
        :class="['ctx-menu-item', { 'ctx-menu-danger': entry.danger }]"
        :data-testid="`resource-menu-${entry.action}`"
        @mouseenter="onEnter(i)"
        @mouseleave="onLeave"
        @click="entry.submenu ? undefined : choose(entry.action)"
      >
        <span class="ctx-menu-arrow">{{ entry.submenu ? '‹' : '' }}</span>{{ tl(entry.label) }}
        <div v-if="entry.submenu && openSub === i" class="ctx-menu-sub">
          <div
            v-for="s in entry.submenu"
            :key="s.arg"
            class="ctx-menu-sub-item"
            @click.stop="choose(entry.action, s.arg)"
          >
            {{ tl(s.label) }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
/**
 * Таблица ресурсов с контекстным меню. Ничего не знает о конкретных действиях:
 * пункты и подменю приходят из menuFor(item), выбранный пункт уходит событием action.
 */

import ContentHeader from '../ContentHeader.vue'
import type { ColumnDef, MenuItemDef } from '../../resources/types'
import './resource.css'
import { ref, reactive, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import RollingValue from './RollingValue.vue'
import { tl } from '../../i18n'
import ColumnsMenu from './ColumnsMenu.vue'
import {
  isSortable, loadPrefs, nextSort, resolveColumns, savePrefs, sortItems,
  type ColumnPrefs, type SortValue,
} from '../../resources/columns'

const props = defineProps<{
  items: any[]
  search: string
  title: string
  /** Ключ сохранённых настроек колонок (по умолчанию title); у списков ресурсов это view */
  prefsKey?: string
  icon?: string
  emptyMessage: string
  columns: ColumnDef[]
  menuFor: (item: any) => MenuItemDef[]
  getItemKey?: (item: any) => string
  refreshing?: boolean
  updatedAt?: string
  /** Колонка чекбоксов + кнопка «−» */
  selectable?: boolean
  /** Ключи выбранных строк (getItemKey) */
  selected?: ReadonlySet<string>
  allSelected?: boolean
  someSelected?: boolean
  /** Кнопка «+» */
  showCreate?: boolean
  createTitle?: string
  /** Скрыть плавающие кнопки (например, пока открыта панель деталей) */
  hideFab?: boolean
}>()

const emit = defineEmits<{
  (e: 'refresh'): void
  (e: 'search', val: string): void
  (e: 'select', item: any): void
  /** Переход к другому ресурсу по ссылке в ячейке */
  (e: 'open', data: { id: string; ns: string; name: string }): void
  /** arg — выбранный элемент подменю (имя контейнера) */
  (e: 'action', action: string, item: any, arg?: string): void
  (e: 'toggle', item: any): void
  (e: 'toggle-all'): void
  (e: 'delete-selected'): void
  (e: 'create'): void
}>()




// ─── Ширина колонок ───
const MIN_COL = 50
const DEFAULT_COL = 140 // ширина колонки, показанной уже после замера раскладки
const ACTION_COL = 56
const SELECT_COL = 40
// Ключ — стабильный идентификатор списка (view), а не переводимый заголовок
const storageKey = () => `kubron:colwidths:${props.prefsKey ?? props.title}`

const tableRef = ref<HTMLTableElement | null>(null)
const widths = reactive<Record<string, number>>({})
const fixed = ref(false)
const resizingKey = ref<string | null>(null)

// ─── Порядок, видимость и сортировка колонок ───
const prefsId = props.prefsKey ?? props.title
const prefs = ref<ColumnPrefs>(loadPrefs(prefsId))
watch(prefs, p => savePrefs(prefsId, p), { deep: true })

const resolved = computed(() => resolveColumns(props.columns, prefs.value))
const visibleColumns = computed(() => resolved.value.visible)

function toggleColumn(key: string) {
  if (key === props.columns[0]?.key) return
  const hidden = new Set(prefs.value.hidden)
  if (!hidden.delete(key)) hidden.add(key)
  prefs.value = { ...prefs.value, hidden: [...hidden] }
}

function moveColumn(from: number, to: number) {
  const keys = resolved.value.ordered.map(c => c.key)
  if (from === to || to < 0 || to >= keys.length) return
  const [k] = keys.splice(from, 1)
  keys.splice(to, 0, k)
  prefs.value = { ...prefs.value, order: keys }
}

// Сортировку оставляем: «сбросить колонки» меняет вид таблицы, а не порядок строк
const resetColumns = () => { prefs.value = { order: [], hidden: [], sort: prefs.value.sort } }

const sortValueOf = (col: ColumnDef, item: any): SortValue =>
  col.sortValue ? col.sortValue(item) : col.formatter ? col.formatter(item) : getCellValue(item, col.key)

// Сортировка по скрытой колонке не действует, но остаётся в настройках до явного клика
const activeSort = computed(() => {
  const s = prefs.value.sort
  const col = s && visibleColumns.value.find(c => c.key === s.key)
  return s && col && isSortable(col) ? { col, dir: s.dir } : null
})

const sortedItems = computed(() => {
  const a = activeSort.value
  return a ? sortItems(props.items, it => sortValueOf(a.col, it), a.dir) : props.items
})

const onSort = (col: ColumnDef) => { prefs.value = { ...prefs.value, sort: nextSort(prefs.value.sort, col.key) } }
const sortIndicator = (col: ColumnDef) =>
  activeSort.value?.col.key === col.key ? (activeSort.value.dir === 'asc' ? ' ▲' : ' ▼') : ''
const ariaSort = (col: ColumnDef) =>
  activeSort.value?.col.key !== col.key ? undefined : activeSort.value.dir === 'asc' ? 'ascending' : 'descending'

const totalWidth = computed(() =>
  visibleColumns.value.reduce((s, c) => s + (widths[c.key] ?? MIN_COL), ACTION_COL + (props.selectable ? SELECT_COL : 0)),
)

function loadSaved() {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey()) || 'null')
    if (saved && visibleColumns.value.every(c => typeof saved[c.key] === 'number')) {
      Object.assign(widths, saved)
      fixed.value = true
    }
  } catch { /* ignore */ }
}
function saveWidths() {
  try { localStorage.setItem(storageKey(), JSON.stringify(widths)) } catch { /* ignore */ }
}

/** Замеряем ширины в авто-раскладке и фиксируем; таблица не выходит за ширину контейнера */
async function freeze() {
  await nextTick()
  const table = tableRef.value
  const ths = table?.querySelectorAll<HTMLElement>('th[data-col]')
  if (!table || !ths?.length) return
  ths.forEach(th => {
    const key = th.dataset.col!
    const def = props.columns.find(c => c.key === key)
    widths[key] = def?.width ?? Math.max(MIN_COL, Math.ceil(th.getBoundingClientRect().width))
  })

  fitToContainer()
  fixed.value = true
}

/** Самая широкая колонка отдаёт излишек, чтобы таблица не вылезала за контейнер */
function fitToContainer() {
  const avail = tableRef.value?.parentElement?.clientWidth ?? 0
  const excess = totalWidth.value - avail
  if (avail && excess > 0 && visibleColumns.value.length) {
    const widest = visibleColumns.value.reduce((a, c) => (widths[c.key] > widths[a.key] ? c : a))
    widths[widest.key] = Math.max(MIN_COL, widths[widest.key] - excess)
  }
}

function resetWidths() {
  try { localStorage.removeItem(storageKey()) } catch { /* ignore */ }
  fixed.value = false
  freeze()
}

let drag: { key: string; startX: number; startW: number } | null = null

function startResize(key: string, e: PointerEvent) {
  if (!fixed.value) return
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  drag = { key, startX: e.clientX, startW: widths[key] }
  resizingKey.value = key
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}
function onDrag(e: PointerEvent) {
  if (!drag) return
  widths[drag.key] = Math.max(MIN_COL, drag.startW + e.clientX - drag.startX)
}
function stopDrag() {
  if (!drag) return
  drag = null
  resizingKey.value = null
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  saveWidths()
}

loadSaved()

// Показали или скрыли колонку: раскладку не перемеряем (ширины пользователя сохраняются),
// новым колонкам даём ширину по умолчанию
watch(
  () => visibleColumns.value.map(c => c.key).join('|'),
  () => {
    if (!fixed.value) return // авто-раскладка сама замерит
    for (const c of visibleColumns.value) if (widths[c.key] === undefined) widths[c.key] = c.width ?? DEFAULT_COL
    fitToContainer()
    saveWidths()
  },
)

onMounted(() => { if (props.items.length && !fixed.value) freeze() })
watch(() => props.items.length, n => { if (n > 0 && !fixed.value) freeze() })

// ─── Ячейки ───

function getCellValue(item: any, key: string): string {
  let value: any = item
  for (const k of key.split('.')) value = value?.[k]
  return value ?? '—'
}
const cellValue = (col: ColumnDef, item: any) => (col.formatter ? col.formatter(item) : getCellValue(item, col.key))
const cellStyle = (col: ColumnDef, item: any) => (col.colorFn ? { color: col.colorFn(item) } : {})
const itemKey = (item: any) => (props.getItemKey ? props.getItemKey(item) : `${item.namespace ?? ''}/${item.name}`)

// ─── Выбор строк ───

const isSelected = (item: any) => !!props.selected?.has(itemKey(item))
const selectedCount = computed(() => props.selected?.size ?? 0)

// «Выбраны некоторые»: у чекбокса нет атрибута indeterminate, только свойство
const allBox = ref<HTMLInputElement | null>(null)
watch(
  () => [props.someSelected, props.items.length] as const,
  () => { if (allBox.value) allBox.value.indeterminate = !!props.someSelected },
  { flush: 'post', immediate: true },
)

// ─── Контекстное меню ───

const menu = ref<{ item: any; entries: MenuItemDef[] } | null>(null)
const menuStyle = ref<Record<string, string>>({})
const openSub = ref<number | null>(null)
let subTimer: ReturnType<typeof setTimeout> | null = null

function clearSubTimer() {
  if (subTimer) clearTimeout(subTimer)
  subTimer = null
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') hideMenu()
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => {
  window.removeEventListener('keydown', onKey)
  clearSubTimer()
})

function onEnter(i: number) {
  clearSubTimer()
  openSub.value = i
}
function onLeave() {
  clearSubTimer()
  subTimer = setTimeout(() => { openSub.value = null }, 150)
}

const MENU_WIDTH = 200

function showMenu(item: any, x: number, y: number) {
  const entries = props.menuFor(item)
  openSub.value = null

  const menuHeight = Math.min(300, entries.length * 30 + 50)

  // не выходить за правый/нижний край окна
  const left = Math.max(0, Math.min(x, window.innerWidth - MENU_WIDTH))
  const top = Math.max(0, Math.min(y, window.innerHeight - menuHeight))

  menuStyle.value = { position: 'fixed', top: `${top}px`, left: `${left}px` }
  menu.value = { item, entries }
}

/** Правая кнопка мыши: меню у курсора */
function openMenuAt(item: any, event: MouseEvent) {
  showMenu(item, event.clientX, event.clientY)
}

/** Кнопка «⋮»: меню слева от кнопки (как раньше) */
function openMenu(item: any, event: MouseEvent) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  const x = rect.left >= MENU_WIDTH ? rect.left - MENU_WIDTH : rect.right
  showMenu(item, x, rect.bottom)
}

function hideMenu() {
  clearSubTimer()
  menu.value = null
}

function choose(action: string, arg?: string) {
  const item = menu.value?.item
  hideMenu()
  if (item) emit('action', action, item, arg)
}
</script>

<style scoped>
.menu-overlay {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  z-index: 9998;
  cursor: default;
}

.cluster-context-menu {
  background: var(--panel-raised);
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  z-index: 9999;
  min-width: 160px;
  overflow: visible;
  padding: 0;
  position: relative;
}

.ctx-menu-item {
  padding: 6px 12px 6px 34px;
  color: var(--text-code);
  cursor: pointer;
  font-size: 0.875rem;
  transition: background 0.15s;
  text-align: left;
  position: relative;
}

.ctx-menu-item:hover {
  background: rgba(255, 255, 255, 0.1);
  color: white;
}

.ctx-menu-item:first-child { border-radius: 3px 3px 0 0; }
.ctx-menu-item:last-child { border-radius: 0 0 3px 3px; }

.ctx-menu-arrow {
  position: absolute;
  left: 19px;
  color: var(--text-gray);
  font-size: 0.9rem;
}

.ctx-menu-sub {
  position: absolute;
  top: 0;
  right: 100%;
  margin-right: 4px;
  background: var(--panel-raised);
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  min-width: 160px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  z-index: 10000;
  white-space: nowrap;
}

.ctx-menu-sub-item {
  padding: 8px 12px;
  color: var(--text-code);
  cursor: pointer;
  font-size: 0.875rem;
  transition: background 0.15s;
  white-space: nowrap;
}

.ctx-menu-sub-item:hover {
  background: rgba(255, 255, 255, 0.1);
  color: white;
}

.ctx-menu-sub-item:first-child { border-radius: 3px 3px 0 0; }
.ctx-menu-sub-item:last-child { border-radius: 0 0 3px 3px; }

.ctx-menu-danger { color: var(--red); }
.ctx-menu-danger:hover { background: rgba(239, 68, 68, 0.2); }

.col-actions { text-align: center; padding-left: 0; padding-right: 0; }
.col-select { width: 40px; text-align: center; padding-left: 0; padding-right: 0; }
.row-check { accent-color: var(--accent); cursor: pointer; margin: 0; vertical-align: middle; }
.row-selected { background: rgba(var(--accent-rgb), 0.08); }
.row-selected:hover { background: rgba(var(--accent-rgb), 0.12) !important; }

/* Плавающие кнопки: выше TabbedPanel (z-index 100), ниже диалогов (10000) */
.fab-group {
  position: fixed;
  right: 24px;
  bottom: 24px;
  display: flex;
  gap: 12px;
  z-index: 101;
}
.fab {
  position: relative;
  width: 46px;
  height: 46px;
  border: none;
  border-radius: 50%;
  font-size: 26px;
  line-height: 1;
  color: #fff;
  cursor: pointer;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
  transition: background 0.15s, opacity 0.15s, transform 0.1s;
}
.fab:active:not(:disabled) { transform: scale(0.95); }
.fab-plus { background: var(--blue-strong); }
.fab-plus:hover { background: var(--blue-deep); }
.fab-minus { background: var(--line-strong); }
.fab-minus:disabled { opacity: 0.55; cursor: not-allowed; }
.fab-minus.fab-danger { background: #dc2626; }
.fab-minus.fab-danger:hover { background: #b91c1c; }
.fab-badge {
  position: absolute;
  top: -4px;
  right: -4px;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--panel-raised);
  border: 1px solid #dc2626;
  font-size: 11px;
  line-height: 16px;
  color: #fff;
}

.clickable-row { cursor: pointer; }
.clickable-row:hover { background: rgba(255, 255, 255, 0.02); }

.btn-icon {
  background: transparent;
  border: none;
  color: var(--text-code);
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: background 0.15s;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: white;
}

.empty {
  padding: 20px;
  text-align: center;
  color: var(--text-gray);
}

td { text-align: left; }

.table-fixed { table-layout: fixed; }
.table-fixed th, .table-fixed td {
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.th-sort {
  display: inline-flex;
  align-items: center;
  padding: 0;
  background: none;
  border: 0;
  font: inherit;
  color: inherit;
  text-transform: inherit;
  letter-spacing: inherit;
  cursor: pointer;
}
.th-sort:hover { color: var(--accent); }
.sort-ind { margin-left: 4px; font-size: 0.7em; }
.col-resizer {
  position: absolute; top: 0; bottom: 0; right: 0; width: 8px;
  cursor: col-resize; touch-action: none;
}
.col-resizer::after {
  content: ''; position: absolute; top: 25%; bottom: 25%; right: 3px; width: 1px;
  background: var(--surface-1);
}
.col-resizer:hover, .col-resizer.active { background: rgba(var(--accent-rgb), 0.35); }
.col-resizer:hover::after, .col-resizer.active::after { background: var(--accent); }
</style>
