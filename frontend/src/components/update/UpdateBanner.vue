<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="bannerVisible && info" class="update-banner" role="status" data-testid="update-banner">
    <span class="update-banner-text">
      {{ $t('update.banner.text', { version: displayVersion(info.latest), current: displayVersion(info.current) }) }}
    </span>
    <button type="button" class="update-banner-btn" @click="openDialog">{{ $t('update.banner.details') }}</button>
    <button type="button" class="update-banner-close" :aria-label="$t('update.banner.dismiss')"
      :title="$t('update.banner.dismiss')" @click="dismissBanner">✕</button>
  </div>
</template>

<script lang="ts" setup>
import { displayVersion, useUpdates } from '../../composables/useUpdates'

const { info, bannerVisible, openDialog, dismissBanner } = useUpdates()
</script>

<style scoped>
.update-banner {
  display: flex; align-items: center; gap: 12px; padding: 6px 16px;
  background: rgba(166, 227, 161, 0.12); border-bottom: 1px solid rgba(166, 227, 161, 0.35);
  color: #cdd6f4; font-size: 13px;
}
.update-banner-text { flex: 1; text-align: left; }
.update-banner-btn {
  background: #a6e3a1; color: #1e1e2e; border: none; border-radius: 6px;
  padding: 4px 12px; font-size: 12px; font-weight: 600; cursor: pointer;
}
.update-banner-close { background: none; border: none; color: #a6adc8; cursor: pointer; font-size: 14px; }
.update-banner-close:hover { color: #cdd6f4; }
</style>
