<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div>
    <div v-if="jobs.length" class="rd-table-wrap">
      <table class="rd-table">
        <thead>
          <tr>
            <th>{{ $t('sections.cronJobHistory.job') }}</th>
            <th>{{ $t('sections.cronJobHistory.duration') }}</th>
            <th>{{ $t('common.age') }}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="j in jobs"
            :key="`${j.namespace}/${j.name}`"
            class="rd-click"
            @click="openJob(j)"
          >
            <td>
              <span class="status-indicator" :class="statusClass(j.status)"></span>
              <span class="job-name">{{ j.name }}</span>
            </td>
            <td class="rd-mono">{{ j.duration || '—' }}</td>
            <td class="rd-mono">{{ j.age }}</td>
            <td @click.stop>
              <button class="btn-icon" @click.stop="openMenu(j, $event)">⋮</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="rd-empty">{{ loading ? $t('common.loading') : $t('sections.cronJobHistory.empty') }}</div>

    <!-- Контекстное меню -->
    <div v-if="menu" :style="menuStyle" class="cluster-context-menu" @click.stop>
      <div class="ctx-menu-item" @click="menuAction('select')">{{ $t('resource.menu.showDetails') }}</div>
      <div class="ctx-menu-item" @click="menuAction('logs')">
        <K8sIcon name="logs" alt="" size="sm" /> {{ $t('defs.menu.logs') }}
      </div>
      <div class="ctx-menu-item" @click="menuAction('edit')">
        <K8sIcon name="edit" alt="" size="sm" /> {{ $t('common.edit') }}
      </div>
      <div class="ctx-menu-item ctx-menu-danger" @click="menuAction('delete')">
        <K8sIcon name="delete" alt="" size="sm" /> {{ $t('common.delete') }}
      </div>
    </div>

    <!-- Оверлей для закрытия меню -->
    <div v-if="menu" class="menu-overlay" @click="hideMenu" @contextmenu.prevent="hideMenu"></div>
  </div>
</template>

<script lang="ts" setup>
import { onMounted, ref, onBeforeUnmount } from 'vue'
import { resourceActions } from '../../../resources/actions'
import K8sIcon from '../../icons/K8sIcon.vue'
import { callApp } from '../../../platform/api'

const props = defineProps<{ resource: any }>()
const emit = defineEmits<{
  (e: 'open', p: { id: string; ns: string; name: string; item?: any }): void
}>()

interface JobRow {
  name: string
  namespace: string
  duration: string
  age: string
  status: string
}

const jobs = ref<JobRow[]>([])
const loading = ref(false)

const menu = ref<{ item: JobRow; x: number; y: number } | null>(null)
const menuStyle = ref({})

async function load() {
  const ns = props.resource?.namespace ?? ''
  const name = props.resource?.name ?? ''
  if (!ns || !name) return

  loading.value = true
  try {
    const raw = (await callApp('GetCronJobJobs', ns, name)) as any[]
    jobs.value = raw.map((j: any) => ({
      name: j.name,
      namespace: j.namespace,
      duration: j.duration,
      age: j.age,
      status: j.status,
    }))
  } catch {
    jobs.value = []
  } finally {
    loading.value = false
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'Complete': return 'status-success'
    case 'Failed': return 'status-failed'
    case 'Running': return 'status-running'
    default: return 'status-unknown'
  }
}

function openJob(j: JobRow) {
  emit('open', { id: 'job', ns: j.namespace, name: j.name })
}

// ─── Context menu ───
function openMenu(item: JobRow, event: MouseEvent) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  menu.value = { item, x: rect.left, y: rect.bottom }
  menuStyle.value = {
    position: 'fixed',
    top: rect.bottom + 'px',
    left: (rect.left >= 160 ? rect.left - 160 : rect.right) + 'px',
    zIndex: '9999',
  }
}

function hideMenu() {
  menu.value = null
}

function menuAction(action: string) {
  const item = menu.value?.item
  hideMenu()
  if (!item) return

  switch (action) {
    case 'select':
      return openJob(item)
    case 'logs':
      return resourceActions.openResourceLogs('job', item.namespace, item.name)
    case 'edit':
      return resourceActions.edit('job', item.namespace, item.name)
    case 'delete':
      return resourceActions.requestDelete('job', item.namespace, item.name)
  }
}

// Закрывать меню по Escape
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') hideMenu()
}

onMounted(() => {
  load()
  document.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
})
</script>

<style scoped>
.status-indicator {
  display: inline-block;
  width: 3px;
  height: 20px;
  border-radius: 2px;
  margin-right: 8px;
  vertical-align: middle;
}
.status-success { background: var(--green); }
.status-failed { background: var(--red); }
.status-running { background: var(--blue); }
.status-unknown { background: var(--text-gray); }

.job-name {
  vertical-align: middle;
}

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
}

.ctx-menu-item {
  padding: 8px 12px;
  color: var(--text-code);
  cursor: pointer;
  font-size: 0.875rem;
  transition: background 0.15s;
  text-align: left;
  display: flex;
  align-items: center;
  gap: 8px;
}
.ctx-menu-item:hover {
  background: rgba(255, 255, 255, 0.1);
  color: white;
}
.ctx-menu-item:first-child { border-radius: 3px 3px 0 0; }
.ctx-menu-item:last-child { border-radius: 0 0 3px 3px; }

.ctx-menu-danger { color: var(--red); }
.ctx-menu-danger:hover { background: rgba(239, 68, 68, 0.2); }
</style>
