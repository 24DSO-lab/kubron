<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="modelValue" class="dialog-overlay" data-testid="confirm-dialog" @click="handleOverlayClick">
    <div class="dialog" @click.stop>
      <div class="dialog-content">
        <p class="dialog-title">{{ title }}</p>
        <p v-if="resourceName" class="resource-name">{{ resourceName }}</p>
        <ul v-if="items?.length" class="item-list">
          <li v-for="it in items" :key="it">{{ it }}</li>
        </ul>
      </div>
      
      <div class="dialog-actions">
        <button class="btn btn-cancel" data-testid="confirm-cancel" @click="handleCancel">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger" data-testid="confirm-accept" @click="handleConfirm" :disabled="isDeleting">
          <span v-if="isDeleting" class="spinner"></span>
          {{ $t('common.confirm') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps<{
  modelValue: boolean
  resourceType: string
  resourceName: string
  namespace?: string
  title?: string
  /** Список объектов под заголовком (массовое удаление) */
  items?: string[]
  isDeleting?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', val: boolean): void
  (e: 'confirm'): void
  (e: 'cancel'): void
}>()

const title = computed(() => {
  if (props.title) return props.title
  return t('utils.confirm.deleteTitle', { type: props.resourceType, name: props.resourceName })
})

function handleCancel() {
  emit('cancel')
  emit('update:modelValue', false)
}

function handleConfirm() {
  emit('confirm')
  if (!props.isDeleting) {
    emit('update:modelValue', false)
  }
}

function handleOverlayClick() {
  emit('cancel')
  emit('update:modelValue', false)
}
</script>

<style scoped>
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10000;
}

.dialog {
  background: var(--panel-raised);
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
  min-width: 400px;
  padding: 16px 20px;
}

.dialog-content {
  margin-bottom: 16px;
}

.dialog-title {
  color: #e0e0e0;
  font-size: 14px;
  margin: 0 0 8px 0;
}

.resource-name {
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 12px;
  color: var(--blue);
  word-break: break-all;
  margin: 0;
}

.item-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 220px;
  overflow-y: auto;
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 12px;
  color: var(--blue);
}

.item-list li {
  padding: 2px 0;
  word-break: break-all;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.btn {
  padding: 8px 16px;
  border: none;
  border-radius: 4px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s;
}

.btn-cancel {
  background: var(--line-strong);
  color: var(--text-code);
}

.btn-cancel:hover {
  background: #4a4d6a;
}

.btn-danger {
  background: #dc2626;
  color: white;
}

.btn-danger:hover {
  background: #b91c1c;
}

.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.spinner {
  width: 12px;
  height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin-right: 6px;
  display: inline-block;
  vertical-align: middle;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
