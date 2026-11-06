<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="settings-card" data-testid="my-connections">
    <div class="settings-card-header">
      <div class="settings-card-header-row">
        <div class="settings-card-header-left">
          <div>
            <h2 class="settings-card-heading">{{ $t('credential.connections.title') }}</h2>
            <p class="settings-card-description">{{ $t('credential.connections.description') }}</p>
          </div>
        </div>
      </div>
    </div>
    <div class="settings-card-body">
      <p v-if="error" class="mc-error" role="alert" data-testid="connections-error">{{ error }}</p>
      <p v-if="loading" role="status">{{ $t('common.loading') }}</p>
      <p v-else-if="items.length === 0" class="mc-empty" data-testid="connections-empty">{{ $t('credential.connections.empty') }}</p>
      <table v-else class="mc-table">
        <thead>
          <tr>
            <th scope="col">{{ $t('credential.connections.cluster') }}</th>
            <th scope="col">{{ $t('credential.connections.method') }}</th>
            <th scope="col">{{ $t('credential.connections.scope') }}</th>
            <th scope="col">{{ $t('credential.connections.expires') }}</th>
            <th scope="col"><span class="mc-sr">{{ $t('credential.connections.actions') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in items" :key="`${c.clusterId}:${c.scope}`" data-testid="connection-row">
            <td>{{ clusterName(c.clusterId) }}<span v-if="c.principal" class="mc-principal"> · {{ c.principal }}</span></td>
            <td>{{ kindLabel(c.kind) }}</td>
            <td>{{ $t(`credential.scope.${c.scope}`) }}</td>
            <td>{{ c.expiresAt ? formatDateTime(c.expiresAt) : $t('credential.connections.noExpiry') }}</td>
            <td>
              <button type="button" class="mc-btn" :data-testid="`disconnect-${c.clusterId}`" :disabled="busyId === c.clusterId"
                :aria-label="$t('credential.connections.disconnectNamed', { name: clusterName(c.clusterId) })"
                @click="disconnect(c.clusterId)">{{ $t('credential.connections.disconnect') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClusters } from '../../composables/useClusters'
import { errorText } from '../../composables/errorText'
import { disconnectCluster, listMyConnections } from '../../platform/credential'
import { formatDateTime } from '../../i18n/format'
import type { ConnectionInfo, CredentialKind } from '../../types/credential'

const { t, te } = useI18n()
const { clusters } = useClusters()
const items = ref<ConnectionInfo[]>([])
const loading = ref(true)
const error = ref('')
const busyId = ref('')

function clusterName(id: string): string {
  return clusters.value.find((c) => c.id === id)?.displayName || id
}

function kindLabel(kind: CredentialKind): string {
  const key = `credential.kind.${kind}`
  return te(key) ? t(key) : kind
}

async function load() {
  try {
    error.value = ''
    items.value = await listMyConnections()
  } catch (e) {
    items.value = []
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

async function disconnect(id: string) {
  busyId.value = id
  try {
    error.value = ''
    await disconnectCluster(id)
    await load()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busyId.value = ''
  }
}

onMounted(load)
</script>

<style scoped>
.mc-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.mc-table th, .mc-table td { padding: 6px 8px; text-align: left; border-bottom: 1px solid #313244; }
.mc-principal { color: #a6adc8; }
.mc-error { color: #f38ba8; }
.mc-empty { color: #a6adc8; }
.mc-btn { padding: 4px 10px; color: #cdd6f4; background: #313244; border: 1px solid #45475a; border-radius: 6px; cursor: pointer; }
.mc-btn:focus-visible { outline: 2px solid #89b4fa; outline-offset: 2px; }
.mc-btn:disabled { opacity: 0.5; }
.mc-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
</style>
