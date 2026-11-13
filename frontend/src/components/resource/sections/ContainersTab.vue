<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div>
    <div v-for="c in containers" :key="c.name" class="ct-block">
      <div class="ct-head" @click="toggle(c.name)">
        <span class="ct-status" :class="statusClass(c)" />
        <span class="ct-name">{{ c.name }}</span>
        <span v-if="c.isEphemeral" class="ct-debug-badge" data-test="ct-debug-badge">{{ $t('sections.containers.debug') }}</span>
        <span class="ct-image">{{ c.image }}</span>
        <span class="ct-arrow" :class="{ open: !collapsed.has(c.name) }">›</span>
      </div>

      <div v-if="!collapsed.has(c.name)" class="ct-body">
        <!-- Metrics -->
        <div v-if="clusterName" class="rd-block">
          <div class="rd-block-head" @click="flip(metricsOpen, c.name)">
            <span>{{ $t('defs.columns.metrics') }}</span>
            <span class="rd-arrow" :class="{ open: metricsOpen[c.name] }">›</span>
          </div>
          <div v-if="metricsOpen[c.name]" class="rd-block-body">
            <PodMetricsViewer
              :cluster-name="clusterName"
              :namespace="resource.namespace"
              :pod-name="resource.name"
              :container-name="c.name"
            />
          </div>
        </div>

        <!-- Status -->
        <div class="rd-row">
          <span class="rd-label">{{ $t('common.status') }}</span>
          <span class="rd-value" :class="statusTextClass(c)">{{ statusText(c) }}</span>
        </div>

        <!-- Image -->
        <div class="rd-row">
          <span class="rd-label">{{ $t('sections.containers.image') }}</span>
          <span class="rd-value"><span v-if="c.image" class="ct-chip">{{ c.image }}</span><template v-else>—</template></span>
        </div>

        <!-- ImagePullPolicy -->
        <div class="rd-row">
          <span class="rd-label">ImagePullPolicy</span>
          <span class="rd-value">{{ c.imagePullPolicy || '—' }}</span>
        </div>

        <!-- Ports -->
        <div v-if="c.ports?.length" class="rd-row">
          <span class="rd-label">{{ $t('defs.columns.ports') }}</span>
          <div class="rd-value ct-ports">
            <div v-for="p in c.ports" :key="p.containerPort + '/' + p.protocol" class="ct-port">
              <a class="rd-link" href="#" @click.prevent>
                {{ p.name ? p.name + ': ' : '' }}{{ p.containerPort }}/{{ p.protocol || 'TCP' }}
              </a>
              <PortForwardControl
                :namespace="resource.namespace"
                :resource-name="resource.name"
                :port="p.containerPort"
                resource-type="pod"
              />
            </div>
          </div>
        </div>

<!-- Environment -->
<div class="rd-row rd-click" @click="toggleEnv(c)">
  <span class="rd-label">{{ $t('sections.containers.environment') }}<span class="rd-arrow" :class="{ open: envOpen[c.name] }">›</span></span>
  <span class="rd-value">{{ $t('sections.containers.envCount', envList(c).length) }}</span>

  <div v-if="envOpen[c.name]" class="rd-wide ct-env-box" @click.stop>
    <div v-if="!envList(c).length" class="rd-muted ct-env-empty">—</div>
    <div v-for="e in sortedEnv(c)" :key="e.name" class="ct-env">
  <span class="ct-env-name" :title="e.name">{{ e.name }}</span>

  <div class="ct-env-val">
    <template v-if="refOf(e.value)">
      <span class="ct-ref-badge" :class="'ct-ref-' + refOf(e.value)!.kind">{{ refOf(e.value)!.kind }}</span>

      <span v-if="envKey(c, e) in secretValues" class="ct-env-plain">{{ secretValues[envKey(c, e)] || $t('sections.containers.emptyValue') }}</span>
      <!-- Ссылка name → key остаётся всегда; у ConfigMap значение выводится отдельной строкой под ней -->
      <span v-else class="ct-ref-text">
        <template v-if="refOf(e.value)!.key">
          {{ refOf(e.value)!.name }}<span class="ct-ref-arrow"> → </span>{{ refOf(e.value)!.key }}
        </template>
        <template v-else>{{ refOf(e.value)!.name }}</template>
      </span>
      <span
        v-if="configMapValue(e) !== undefined"
        class="ct-env-plain ct-env-sub"
      >{{ configMapValue(e) || $t('sections.containers.emptyValue') }}</span>
    </template>
    <span v-else class="ct-env-plain">{{ e.value || '—' }}</span>

    <div v-if="secretErrors[envKey(c, e)]" class="ct-env-err">{{ secretErrors[envKey(c, e)] }}</div>
  </div>

  <div class="ct-env-act">
    <button
      v-if="refOf(e.value)?.kind === 'secret' && can(Perm.SecretsRead)"
      class="ct-env-eye"
      :disabled="secretLoading[envKey(c, e)]"
      :title="envKey(c, e) in secretValues ? $t('sections.containers.hide') : $t('sections.containers.reveal')"
      @click.stop="toggleSecret(c, e)"
    >
      <K8sIcon :name="envKey(c, e) in secretValues ? 'view-off' : 'view'" size="sm" />
    </button>
  </div>
</div>
  </div>
</div>

        <!-- Mounts -->
        <div class="rd-row rd-click" @click="flip(mountsClosed, c.name)">
          <span class="rd-label">{{ $t('sections.containers.mounts') }}<span class="rd-arrow" :class="{ open: !mountsClosed[c.name] }">›</span></span>
          <span v-if="mountsClosed[c.name]" class="rd-value">{{ $t('sections.containers.mountsCount', mountList(c).length) }}</span>
          <div v-else class="rd-value ct-mounts" @click.stop>
            <div v-if="!mountList(c).length" class="rd-muted">—</div>
            <div v-for="m in mountList(c)" :key="m.mountPath + m.name" class="ct-mount">
              <span class="ct-chip ct-chip-block">{{ m.mountPath }}</span>
              <span class="ct-mount-from">
                {{ $t('sections.containers.mountFrom', { name: m.name }) }}<template v-if="m.subPath"> ({{ m.subPath }})</template>
                ({{ m.readOnly ? 'ro' : 'rw' }})
              </span>
            </div>
          </div>
        </div>

        <!-- Requests / Limits -->
        <div class="rd-row">
          <span class="rd-label">{{ $t('sections.containers.requests') }}</span>
          <span class="rd-value">{{ fmtRes(c, 'requests') }}</span>
        </div>
        <div class="rd-row">
          <span class="rd-label">{{ $t('defs.columns.limits') }}</span>
          <span class="rd-value">{{ fmtRes(c, 'limits') }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, reactive, ref } from 'vue'
import { clusterNameKey } from '../../../resources/context'
import PodMetricsViewer from '../../metrics/PodMetricsViewer.vue'
import K8sIcon from '../../icons/K8sIcon.vue'
import PortForwardControl from './PortForwardControl.vue'
import { callApp } from '../../../resources/wails'
import { useNotifications } from '../../../composables/useNotifications'
import { can } from '../../../composables/useAuth'
import { Perm } from '../../../types/auth'
import { t } from '../../../i18n'
import { errorText } from '../../../composables/errorText'

const props = defineProps<{ resource: any }>()
const clusterName = inject(clusterNameKey, ref(undefined))
const containers = computed<any[]>(() => props.resource?.containers ?? [])

// Состояния раскрытия: metrics/env свёрнуты по умолчанию, mounts раскрыт
const collapsed = ref<Set<string>>(new Set())
const metricsOpen = reactive<Record<string, boolean>>({})
const envOpen = reactive<Record<string, boolean>>({})
const mountsClosed = reactive<Record<string, boolean>>({})
// const envRevealed = reactive<Record<string, boolean>>({})

function toggle(name: string) {
  const s = new Set(collapsed.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  collapsed.value = s
}
function flip(rec: Record<string, boolean>, key: string) {
  rec[key] = !rec[key]
}

// Go отдаёт env и volumeMounts (а не environment / mounts)
const envList = (c: any): { name: string; value: string }[] => c.env ?? c.environment ?? []
const mountList = (c: any): any[] => c.volumeMounts ?? c.mounts ?? []

const isRef = (v: string) => typeof v === 'string' && v.startsWith('secret(')

/** "configmap(name/key)" -> { kind, name, key } */
function refOf(v: string): { kind: string; name: string; key: string } | null {
  const m = typeof v === 'string' ? v.match(/^(secret|configmap|field|resource)\((.*)\)$/) : null
  if (!m) return null
  const i = m[2].indexOf('/')
  return i < 0
    ? { kind: m[1], name: m[2], key: '' }
    : { kind: m[1], name: m[2].slice(0, i), key: m[2].slice(i + 1) }
}

const sortedEnv = (c: any) => [...envList(c)].sort((a, b) => a.name.localeCompare(b.name))

const notifications = useNotifications()
const secretValues = reactive<Record<string, string>>({}) // загруженные значения
const secretLoading = reactive<Record<string, boolean>>({})

const envKey = (c: any, e: { name: string }) => `${c.name}/${e.name}`

const secretErrors = reactive<Record<string, string>>({})

async function toggleSecret(c: any, e: { name: string; value: string }) {
  const k = envKey(c, e)
  if (k in secretValues) { delete secretValues[k]; return } // скрыть
  if (secretLoading[k]) return

  const ref = refOf(e.value)
  if (!ref || ref.kind !== 'secret' || !ref.key) return

  secretLoading[k] = true
  delete secretErrors[k]
  try {
    const v = await callApp('GetSecretValue', props.resource.namespace, ref.name, ref.key)
    secretValues[k] = v ?? ''
  } catch (err: any) {
    console.error('GetSecretValue failed:', err)
    secretErrors[k] = errorText(err)
  } finally {
    secretLoading[k] = false
  }
}

// ConfigMap не секрет: значения env с configMapKeyRef подгружаются автоматически
// при раскрытии блока «Окружение» (через GetConfigMapDetail, право read).
// Ключ кеша — имя ConfigMap; undefined в configMapValue => показываем "name → key".
const configMapData = reactive<Record<string, Record<string, string>>>({})

async function loadConfigMaps(c: any) {
  const names = new Set<string>()
  for (const e of envList(c)) {
    const r = refOf(e.value)
    if (r?.kind === 'configmap' && r.key) names.add(r.name)
  }
  await Promise.all([...names].map(async (name) => {
    try {
      const cm = await callApp('GetConfigMapDetail', props.resource.namespace, name)
      configMapData[name] = cm?.data ?? {}
    } catch (err) {
      // нет прав / ConfigMap удалён / optional — остаёмся на отображении ссылки
      console.warn('GetConfigMapDetail failed:', name, err)
      delete configMapData[name]
    }
  }))
}

function configMapValue(e: { value: string }): string | undefined {
  const r = refOf(e.value)
  if (r?.kind !== 'configmap') return undefined
  return configMapData[r.name]?.[r.key]
}

function toggleEnv(c: any) {
  flip(envOpen, c.name)
  if (envOpen[c.name]) void loadConfigMaps(c) // обновляем значения при каждом раскрытии
}

// function maskRef(v: string): string {
//   const m = v.match(/^secret\(([^)]+)\)$/)
//   return m ? `secret(${m[1].split('/')[0]})` : 'secret(???)'
// }

function statusText(c: any): string {
  const state = c.state || t('common.unknown')
  return c.ready ? t('sections.containers.stateReady', { state }) : state
}
function statusClass(c: any): string {
  // У ephemeral-контейнера Kubernetes не ведёт готовность: работающий отладочный считается исправным
  if (c.state === 'running' && (c.ready || c.isEphemeral)) return 'ct-ok'
  if (c.state === 'running') return 'ct-warn'
  if (['CrashLoopBackOff', 'Error', 'OOMKilled'].includes(c.state)) return 'ct-err'
  return 'ct-neutral'
}
function statusTextClass(c: any): string {
  return 'ct-text-' + statusClass(c).slice(3)
}

// resources: { requestsCpu, requestsMemory, limitsCpu, limitsMemory }
function fmtRes(c: any, kind: 'requests' | 'limits'): string {
  const r = c.resources
  if (!r) return '—'
  const cpu = kind === 'requests' ? r.requestsCpu : r.limitsCpu
  const mem = kind === 'requests' ? r.requestsMemory : r.limitsMemory
  const parts: string[] = []
  if (cpu) parts.push(`${t('defs.columns.cpu')}: ${cpu}`)
  if (mem) parts.push(`${t('defs.columns.memory')}: ${mem}`)
  return parts.length ? parts.join(', ') : '—'
}
</script>

<style scoped>
.ct-block { margin-bottom: 12px; border: 1px solid var(--line); border-radius: 6px; overflow: hidden; background: var(--panel); }
.ct-head {
  display: flex; align-items: center; gap: 10px;
  padding: 12px 16px; cursor: pointer; user-select: none;
  font-weight: 600; font-size: 0.95rem; color: var(--text-code);
  background: linear-gradient(135deg, rgba(59, 130, 246, 0.1) 0%, rgba(59, 130, 246, 0.05) 100%);
}
.ct-head:hover {
  background: linear-gradient(135deg, rgba(59, 130, 246, 0.15) 0%, rgba(59, 130, 246, 0.1) 100%);
}
.ct-status { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
.ct-ok { background: var(--green); } .ct-warn { background: var(--yellow); } .ct-err { background: var(--red); } .ct-neutral { background: var(--text-gray); }
.ct-text-ok { color: var(--green); } .ct-text-warn { color: var(--yellow); } .ct-text-err { color: var(--red); } .ct-text-neutral { color: var(--text-gray); }
.ct-name { color: var(--text-code); }
.ct-debug-badge { font-size: 0.7rem; font-weight: 600; padding: 1px 6px; border-radius: 3px; background: rgba(59, 130, 246, 0.15); color: var(--blue); border: 1px solid rgba(59, 130, 246, 0.35); }
.ct-image { color: var(--text-gray); font-size: 0.8rem; font-weight: 400; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ct-arrow { margin-left: auto; transition: transform 0.2s; color: var(--text-gray); } .ct-arrow.open { transform: rotate(90deg); }
.ct-body { padding: 16px; border-top: 1px solid var(--line); display: flex; flex-direction: column; }
.ct-body > .rd-block { margin-top: 8px; }

.ct-chip {
  display: inline-block; padding: 3px 8px; background: var(--panel); border-radius: 3px;
  font-family: 'Courier New', monospace; font-size: 0.8rem; color: var(--text-code); word-break: break-all;
}
.ct-chip-block { display: block; }

.ct-ports { display: flex; flex-direction: column; gap: 6px; }
.ct-port { display: flex; align-items: center; justify-content: space-between; gap: 8px; }

.ct-env-box {
  margin-top: 8px; max-height: 340px; overflow-y: auto;
  background: var(--panel); border: 1px solid var(--line); border-radius: 6px;
}
.ct-env-empty { padding: 10px 12px; }

.ct-env:last-child { border-bottom: none; }
.ct-env:hover { background: rgba(255,255,255,0.02); }

.ct-env-plain { color: #e5e7eb; font-family: 'Courier New', monospace; overflow-wrap: anywhere; }
.ct-env {
  display: grid; grid-template-columns: minmax(130px, 38%) minmax(0, 1fr) 26px; gap: 10px;
  padding: 7px 12px; border-bottom: 1px solid rgba(255,255,255,0.05);
  font-size: 0.82rem; align-items: start;
}
.ct-env-name {
  color: var(--blue); font-family: 'Courier New', monospace; font-weight: 600;
  overflow-wrap: break-word; word-break: break-word; min-width: 0;
}
.ct-env-val { display: flex; align-items: baseline; flex-wrap: wrap; gap: 6px; min-width: 0; }
.ct-env-act { display: flex; justify-content: flex-end; }
.ct-env-err { flex-basis: 100%; color: var(--red-soft); font-size: 0.75rem; }

.ct-ref-badge {
  flex-shrink: 0; padding: 1px 6px; border-radius: 3px;
  font-size: 0.68rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em;
}
.ct-ref-configmap { color: var(--blue); background: rgba(96,165,250,0.14); }
.ct-ref-secret    { color: var(--amber); background: rgba(251,191,36,0.14); }
.ct-ref-field, .ct-ref-resource { color: #a78bfa; background: rgba(167,139,250,0.14); }

.ct-ref-text { color: var(--text-soft); overflow-wrap: anywhere; min-width: 0; }
.ct-env-sub { flex-basis: 100%; }
.ct-ref-arrow { color: var(--text-dim); }

.ct-env-eye {
  display: inline-flex; align-items: center; justify-content: center;
  width: 22px; height: 22px; padding: 0; flex-shrink: 0;
  background: transparent; border: none; border-radius: 3px;
  cursor: pointer; color: var(--text-gray); transition: all 0.15s;
}
.ct-env-eye:hover { background: rgba(255,255,255,0.08); color: var(--text-code); }
.ct-env-eye:disabled { opacity: 0.5; cursor: wait; }

.ct-mounts { display: flex; flex-direction: column; gap: 6px; }
.ct-mount { display: flex; flex-direction: column; gap: 4px; }
.ct-mount-from { color: var(--text-code); font-size: 0.875rem; }
</style>