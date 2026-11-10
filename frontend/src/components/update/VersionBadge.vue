<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <button v-if="version" type="button" class="version-badge" data-testid="version-badge"
    :class="{ 'has-update': updateAvailable }" :title="title" :aria-label="title" @click="openDialog">
    <span class="version-text">{{ displayVersion(version.version) }}</span>
    <span v-if="updateAvailable" class="version-dot" aria-hidden="true"></span>
  </button>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { displayVersion, useUpdates } from '../../composables/useUpdates'

const { t } = useI18n()
const { version, info, updateAvailable, openDialog } = useUpdates()

const title = computed(() =>
  updateAvailable.value
    ? t('update.badge.available', { version: displayVersion(info.value?.latest) })
    : t('update.badge.title'),
)
</script>

<style scoped>
.version-badge {
  display: inline-flex; align-items: center; gap: 6px; margin-right: 12px;
  background: transparent; border: 1px solid #313244; border-radius: 999px;
  padding: 3px 10px; color: #a6adc8; font-size: 12px; cursor: pointer;
}
.version-badge:hover { border-color: #45475a; color: #cdd6f4; }
.version-badge.has-update { border-color: #a6e3a1; color: #a6e3a1; }
.version-dot { width: 7px; height: 7px; border-radius: 50%; background: #a6e3a1; }
</style>
