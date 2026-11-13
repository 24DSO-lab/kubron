<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="scale-dialog-overlay" @click="handleOverlayClick">
    <div class="scale-dialog" @click.stop>
      <div class="dialog-header">
        <h3>{{ $t('panels.scale.title', { kind: deployment?._kind || 'Deployment' }) }} <span class="resource-name">{{ deployment?.namespace }}/{{ deployment?.name }}</span></h3>
      </div>

      <div class="dialog-content">
        <div class="scale-info">
          <div class="info-row">
            <span class="info-label">{{ $t('panels.scale.current') }}</span>
            <span class="info-value">{{ deployment?.replicas ?? deployment?.desired }}</span>
          </div>
          <div class="info-row">
            <span class="info-label">{{ $t('panels.scale.desired') }}</span>
            <span class="info-value">{{ desiredReplicas }}</span>
          </div>
        </div>

        <div class="slider-container">
          <input
            v-model.number="desiredReplicas"
            type="range"
            class="slider"
            :min="0"
            :max="maxReplicas"
          />
          <div class="slider-controls">
            <button class="slider-btn minus-btn" @click="desiredReplicas = Math.max(0, desiredReplicas - 1)" :title="$t('panels.scale.decrease')">−</button>
            <button class="slider-btn plus-btn" @click="desiredReplicas = Math.min(maxReplicas, desiredReplicas + 1)" :title="$t('panels.scale.increase')">+</button>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-cancel" @click="handleCancel">{{ $t('common.cancel') }}</button>
        <button class="btn btn-scale" @click="handleScale" :disabled="loading">
          {{ loading ? $t('panels.scale.scaling') : $t('panels.scale.submit') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref } from 'vue'
import type { DeploymentInfo } from '../../types'

const props = defineProps<{
  deployment: (DeploymentInfo & { _kind?: string; desired?: number }) | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'scale', replicas: number): void
}>()

const desiredReplicas = ref(props.deployment?.replicas ?? props.deployment?.desired ?? 1)
const loading = ref(false)
const maxReplicas = ref(20)

function handleOverlayClick() {
  handleCancel()
}

function handleCancel() {
  emit('close')
}

async function handleScale() {
  if (!props.deployment) return
  
  try {
    loading.value = true
    emit('scale', desiredReplicas.value)
    // Закроем диалог после успеха (сигнал придёт снаружи если нужно)
    setTimeout(() => {
      emit('close')
    }, 500)
  } catch (e) {
    console.error('Scale error:', e)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.scale-dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2000;
}

.scale-dialog {
  background: var(--panel-raised);
  border: 1px solid var(--line-strong);
  border-radius: 8px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5);
  width: 90%;
  max-width: 500px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.dialog-header {
  padding: 16px;
  border-bottom: 1px solid var(--line-strong);
  background: var(--panel-deep);
}

.dialog-header h3 {
  margin: 0;
  color: white;
  font-size: 1rem;
  font-weight: 600;
}

.resource-name {
  color: var(--blue);
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
}

.dialog-content {
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.scale-info {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
}

.info-label {
  color: var(--text-soft);
  font-size: 0.9rem;
  font-weight: 500;
}

.info-value {
  color: var(--blue);
  font-size: 1rem;
  font-weight: 600;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
}

.slider-container {
  display: flex;
  align-items: center;
  gap: 12px;
}

.slider {
  flex: 1;
  height: 6px;
  border-radius: 3px;
  background: linear-gradient(
    to right,
    var(--blue-strong) 0%,
    var(--blue-strong) calc(var(--value, 0) * 100%),
    var(--line-strong) calc(var(--value, 0) * 100%),
    var(--line-strong) 100%
  );
  outline: none;
  -webkit-appearance: none;
  appearance: none;
  cursor: pointer;
}

.slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--blue-strong);
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(59, 130, 246, 0.4);
  transition: all 0.15s;
}

.slider::-webkit-slider-thumb:hover {
  transform: scale(1.2);
  box-shadow: 0 2px 12px rgba(59, 130, 246, 0.6);
}

.slider::-moz-range-thumb {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--blue-strong);
  border: none;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(59, 130, 246, 0.4);
  transition: all 0.15s;
}

.slider::-moz-range-thumb:hover {
  transform: scale(1.2);
  box-shadow: 0 2px 12px rgba(59, 130, 246, 0.6);
}

.slider-controls {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.slider-btn {
  width: 32px;
  height: 32px;
  border-radius: 4px;
  border: 1px solid var(--line-strong);
  background: var(--panel);
  color: var(--text-soft);
  font-size: 1rem;
  cursor: pointer;
  transition: all 0.15s;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
}

.slider-btn:hover {
  background: rgba(59, 130, 246, 0.1);
  border-color: var(--blue-strong);
  color: var(--blue-strong);
}

.minus-btn {
  /* Icon − */
}

.plus-btn {
  /* Icon + */
}

.dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-top: 1px solid var(--line-strong);
  background: var(--panel-deep);
}

.btn {
  padding: 8px 16px;
  border-radius: 4px;
  border: none;
  font-weight: 600;
  font-size: 0.9rem;
  cursor: pointer;
  transition: all 0.15s;
  white-space: nowrap;
}

.btn-cancel {
  background: transparent;
  border: 1px solid var(--line-strong);
  color: var(--text-soft);
}

.btn-cancel:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: #5a5c7a;
  color: var(--text-code);
}

.btn-scale {
  background: var(--blue-strong);
  color: white;
  margin-left: auto;
}

.btn-scale:hover:not(:disabled) {
  background: var(--blue-deep);
}

.btn-scale:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
