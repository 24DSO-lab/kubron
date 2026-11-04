<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="settings-page">
    <h1 class="settings-title">{{ $t('settings.title') }}</h1>

    <!-- Language -->
    <div class="settings-card">
      <div class="settings-card-header">
        <div class="settings-card-header-row">
          <div class="settings-card-header-left">
            <div class="settings-card-icon-wrap blue">
              <svg class="settings-card-icon blue" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                  d="M3 5h12M9 3v2m1.048 9.5A18.022 18.022 0 016.412 9m6.088 9h7M11 21l5-10 5 10M12.751 5C11.783 10.77 8.07 15.61 3 18.129" />
              </svg>
            </div>
            <div>
              <h2 class="settings-card-heading">{{ $t('settings.language.title') }}</h2>
              <p class="settings-card-description">{{ $t('settings.language.description') }}</p>
            </div>
          </div>
          <LanguageSwitcher />
        </div>
      </div>
    </div>

    <MyConnections />

    <!-- App Settings -->
    <div class="settings-card">
      <div class="settings-card-header">
        <div class="settings-card-header-row">
          <div class="settings-card-header-left">
            <div class="settings-card-icon-wrap blue">
              <svg class="settings-card-icon blue" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                  d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
              </svg>
            </div>
            <div>
              <h2 class="settings-card-heading">{{ $t('settings.refresh.title') }}</h2>
              <p class="settings-card-description">{{ $t('settings.refresh.description') }}</p>
            </div>
          </div>
        </div>
      </div>

      <div class="settings-card-body">
        <div class="settings-setting-row">
          <div class="settings-setting-field">
            <label class="settings-label">
              {{ $t('settings.refresh.interval') }}
            </label>
            <div class="settings-setting-row-input">
              <input
                v-model.number="refreshInterval"
                type="number"
                min="0"
                max="300"
                class="settings-input"
                placeholder="10"
              />
              <span class="settings-input-placeholder">{{ $t('settings.refresh.seconds') }}</span>
            </div>
            <p class="settings-hint">
              {{ $t('settings.refresh.hint') }}
            </p>
          </div>
          <button
            @click="saveRefreshSettings"
            :disabled="!settingsLoaded"
            class="settings-save-btn"
          >
            {{ $t('common.save') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Metrics Stack -->
    <div class="settings-card">
      <!-- Header -->
      <div class="settings-card-header">
        <div class="settings-card-header-row">
          <div class="settings-card-header-left">
            <div class="settings-card-icon-wrap purple">
              <svg class="settings-card-icon purple" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                  d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
              </svg>
            </div>
            <div>
              <h2 class="settings-card-heading">{{ $t('settings.metrics.title') }}</h2>
              <p class="settings-card-description">Prometheus + Node Exporter + Kube State Metrics</p>
            </div>
          </div>
          <MetricsStatusBadge v-if="status" :status="status.status" />
        </div>
      </div>

      <div class="settings-card-body">
        <!-- NOT INSTALLED -->
        <div v-if="currentState === 'not_installed'">
          <i18n-t keypath="settings.metrics.installHint" tag="p" class="settings-not-installed-text">
            <template #namespace><code class="settings-code-inline">kubron-metrics</code></template>
          </i18n-t>

          <div class="settings-components-list">
            <h4 class="settings-components-title">
              {{ $t('settings.metrics.components') }}
            </h4>
            <div class="settings-components-list-inner">
              <div v-for="c in componentsList" :key="c.name"
                class="settings-component-item">
                <span class="w-2 h-2 rounded-full" :class="c.dotColor"></span>
                <span class="text-gray-300 font-medium">{{ c.name }}</span>
                <span class="text-gray-600 text-xs">— {{ c.desc }}</span>
              </div>
            </div>
          </div>

          <button v-if="canModify" @click="handleInstall" :disabled="loading"
            class="settings-install-btn">
            <svg v-if="loading" class="animate-spin h-3.5 w-3.5" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10"
                stroke="currentColor" stroke-width="3"/>
              <path class="opacity-75" fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/>
            </svg>
            {{ loading ? $t('settings.metrics.installing') : $t('settings.metrics.install') }}
          </button>
          <p v-else class="settings-not-installed-text">{{ $t('settings.metrics.adminOnly') }}</p>
        </div>

        <!-- INSTALLING -->
        <div v-else-if="currentState === 'installing'">
          <div class="settings-installing-status">
            <div class="settings-installing-spinner"></div>
            <p class="settings-installing-text">
              {{ $t('settings.metrics.installingStatus') }}
            </p>
          </div>
        </div>

        <!-- INSTALLED -->
        <div v-else-if="currentState === 'installed'">
          <p class="settings-installed-status-text">
            {{ $t('settings.metrics.running') }}
          </p>

          <!-- Running components -->
          <div v-if="status?.components"
            class="settings-installed-components">
            <h4 class="settings-installed-components-title">
              {{ $t('settings.metrics.runningComponents') }}
            </h4>
            <div class="settings-installed-components-inner">
              <div v-for="(comp, name) in status.components" :key="name"
                class="settings-installed-comp-item">
                <div class="flex items-center gap-2.5">
                  <span :class="['w-2 h-2 rounded-full',
                    comp.ready ? 'bg-green-400' : 'bg-yellow-400 animate-pulse']">
                  </span>
                  <span class="text-gray-300">{{ fmtName(name as string) }}</span>
                </div>
                <span class="text-gray-500 text-xs">
                  {{ $t('settings.metrics.ready', { available: comp.availableReplicas, total: comp.replicas }) }}
                </span>
              </div>
            </div>
          </div>

          <!-- Uninstall -->
          <div v-if="canModify" class="settings-uninstall-section">
            <div class="settings-uninstall-row">
              <div>
                <p class="text-sm text-gray-300">{{ $t('settings.metrics.uninstall') }}</p>
                <p class="text-xs text-gray-500 mt-0.5">
                  {{ $t('settings.metrics.uninstallHint') }}
                </p>
              </div>
              <div v-if="!showConfirm">
                <button @click="showConfirm = true"
                  class="settings-delete-btn">
                  {{ $t('common.delete') }}
                </button>
              </div>
              <div v-else class="settings-confirm-bar">
                <span class="settings-confirm-text">{{ $t('settings.metrics.confirm') }}</span>
                <button @click="showConfirm = false"
                  class="settings-cancel-btn">
                  {{ $t('common.cancel') }}
                </button>
                <button @click="handleUninstall" :disabled="loading"
                  class="settings-confirm-btn">
                  {{ $t('settings.metrics.confirmUninstall') }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- DEGRADED -->
        <div v-else-if="currentState === 'degraded'">
          <div class="settings-degraded-warning">
            <div class="settings-degraded-warning-content">
              <svg class="w-5 h-5 text-yellow-400 flex-shrink-0 mt-0.5" fill="none"
                stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                  d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667
                     1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732
                     0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z"/>
              </svg>
              <div>
                <p class="text-sm text-yellow-300 font-medium">{{ $t('settings.metrics.degraded') }}</p>
                <p class="text-xs text-yellow-400/70 mt-1">{{ status?.message }}</p>
              </div>
            </div>
          </div>

          <div v-if="status?.components" class="settings-degraded-components">
            <div class="settings-degraded-components-inner">
              <div v-for="(comp, name) in status.components" :key="name"
                class="settings-degraded-comp-item">
                <div class="flex items-center gap-2.5">
                  <span :class="['w-2 h-2 rounded-full',
                    comp.ready ? 'bg-green-400' : 'bg-red-400']"></span>
                  <span class="text-gray-300">{{ fmtName(name as string) }}</span>
                </div>
                <span :class="['text-xs', comp.ready ? 'text-gray-500' : 'text-red-400']">
                  {{ $t('settings.metrics.ready', { available: comp.availableReplicas, total: comp.replicas }) }}
                </span>
              </div>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button @click="fetchStatus"
              class="settings-refresh-btn">
              {{ $t('common.refresh') }}
            </button>
            <button v-if="canModify" @click="handleReinstall" :disabled="loading"
              class="settings-reinstall-btn">
              {{ $t('settings.metrics.reinstall') }}
            </button>
            <button v-if="canModify" @click="showConfirm = true"
              class="settings-delete-btn">
              {{ $t('common.delete') }}
            </button>
          </div>

          <div v-if="showConfirm" class="settings-confirm-bar">
            <span class="settings-confirm-text">{{ $t('settings.metrics.confirm') }}</span>
            <button @click="showConfirm = false"
              class="settings-cancel-btn">
              {{ $t('common.cancel') }}
            </button>
            <button @click="handleUninstall" :disabled="loading"
              class="settings-confirm-btn">
              {{ $t('common.confirm') }}
            </button>
          </div>
        </div>

        <!-- Error -->
        <div v-if="error" class="settings-error-box">
          <p class="settings-error-text">{{ error }}</p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import LanguageSwitcher from '../LanguageSwitcher.vue'
import MetricsStatusBadge from '../metrics/MetricsStatusBadge.vue'
import { useMetricsStatus } from '../../composables/useMetrics'
import { useAppSettings } from '../../composables/useAppSettings'
import { can } from '../../composables/useAuth'
import { Perm } from '../../types/auth'
import MyConnections from '../credential/MyConnections.vue'

const props = defineProps<{
  clusterName: string
}>()

const { t } = useI18n()

// Установка и удаление стека метрик изменяют кластер
const canModify = computed(() => can(Perm.Write))

const {
  status,
  loading,
  error,
  fetchStatus,
  install,
  uninstall,
} = useMetricsStatus(props.clusterName)

const appSettings = useAppSettings()
const refreshInterval = ref(10)
const settingsLoaded = ref(false)

const showConfirm = ref(false)

const currentState = computed(() => {
  if (!status.value) return 'not_installed'
  return status.value.status
})

const componentsList = computed(() => [
  { name: 'Prometheus', desc: t('settings.metrics.componentPrometheus'), dotColor: 'bg-orange-400' },
  { name: 'Node Exporter', desc: t('settings.metrics.componentNodeExporter'), dotColor: 'bg-green-400' },
  { name: 'Kube State Metrics', desc: t('settings.metrics.componentKubeState'), dotColor: 'bg-blue-400' },
])

function fmtName(raw: string): string {
  return raw
    .replace(/-/g, ' ')
    .replace(/\b\w/g, c => c.toUpperCase())
}

async function handleInstall() {
  await install()
}

async function handleUninstall() {
  showConfirm.value = false
  await uninstall()
}

async function handleReinstall() {
  await uninstall()
  await install()
}

async function saveRefreshSettings() {
  await appSettings.saveSettings({
    autoRefreshInterval: refreshInterval.value,
  })
}

onMounted(async () => {
  fetchStatus()
  await appSettings.loadSettings()
  refreshInterval.value = appSettings.settings.value.autoRefreshInterval || 10
  settingsLoaded.value = true
})
</script>
