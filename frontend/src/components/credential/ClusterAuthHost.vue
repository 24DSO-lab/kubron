<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <ChallengeDialog v-if="auth.challenge.value" :key="auth.challenge.value.id" :challenge="auth.challenge.value"
    :busy="auth.busy.value" :error="auth.error.value" :attempt="auth.attempt.value"
    :remaining-sec="auth.remainingSec.value" :expired="auth.expired.value"
    @submit="auth.submit" @cancel="auth.cancel" @restart="auth.restart" @open-url="openUrl" />
</template>

<script lang="ts" setup>
import { onMounted, onUnmounted } from 'vue'
import { useClusterAuth } from '../../composables/useClusterAuth'
import { openUrl } from '../../platform/files'
import ChallengeDialog from './ChallengeDialog.vue'

const emit = defineEmits<{ (e: 'required', clusterId: string): void }>()
const auth = useClusterAuth()
let stop: (() => void) | null = null

onMounted(() => { stop = auth.listen((id) => emit('required', id)) })
onUnmounted(() => { stop?.() })
</script>
