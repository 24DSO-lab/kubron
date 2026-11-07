<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="cl-overlay" @mousedown.self="pressedOverlay = true" @click.self="onOverlayClick">
    <div class="cl-dialog" role="dialog" aria-modal="true" :aria-labelledby="`${uid}-title`">
      <header class="cl-head">
        <ClusterAvatar :name="name" :icon="icon" :color="color" :environment="environment" :size="44" show-env />
        <div class="cl-head-text">
          <div :id="`${uid}-title`" class="cl-title">{{ name.trim() || $t('cluster.dialog.newTitle') }}</div>
          <div class="cl-subtitle cl-mono">{{ subtitle }}</div>
        </div>
        <button type="button" class="cl-close" :aria-label="$t('common.close')" @click="emit('close')">
          <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
            <path d="M2 2l10 10M12 2L2 12" />
          </svg>
        </button>
      </header>

      <div class="cl-body">
        <section class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionMain') }}</h3>
          <div class="cl-field">
            <label :for="`${uid}-name`" class="cl-label">{{ $t('cluster.dialog.name') }}</label>
            <input :id="`${uid}-name`" ref="nameInput" v-model="name" class="cl-input" :maxlength="MAX_CLUSTER_NAME"
              :placeholder="$t('cluster.dialog.namePlaceholder')" autocomplete="off" spellcheck="false"
              @keydown.enter.prevent="submit" />
          </div>
        </section>

        <section v-if="capabilities.teams" class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionTeam') }}</h3>
          <div class="cl-field">
            <label :for="`${uid}-team`" class="cl-label">{{ $t('cluster.dialog.team') }}</label>
            <select :id="`${uid}-team`" v-model="teamId" class="cl-input">
              <option v-for="tm in adminTeams" :key="tm.id" :value="tm.id">{{ tm.name }}</option>
            </select>
            <p class="cl-hint" :class="{ 'is-error': !adminTeams.length }">
              {{ adminTeams.length ? $t('cluster.dialog.teamHint') : $t('cluster.dialog.noAdminTeams') }}
            </p>
          </div>
        </section>

        <section class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionConnection') }}</h3>

          <div class="cl-field">
            <div class="cl-segment cl-method" role="radiogroup" :aria-label="$t('cluster.dialog.kubeconfigSource')">
              <button v-if="capabilities.fileDialog" type="button" role="radio" class="cl-segment-item"
                :class="{ on: method === 'file' }"
                :aria-checked="method === 'file'" @click="setMethod('file')">{{ $t('cluster.dialog.methodFile') }}</button>
              <button type="button" role="radio" class="cl-segment-item" :class="{ on: method === 'text' }"
                :aria-checked="method === 'text'" @click="setMethod('text')">{{ $t('cluster.dialog.methodText') }}</button>
              <button v-if="canInCluster" type="button" role="radio" class="cl-segment-item"
                :class="{ on: method === 'in-cluster' }"
                :aria-checked="method === 'in-cluster'" @click="setMethod('in-cluster')">{{ $t('cluster.dialog.methodInCluster') }}</button>
            </div>

            <p v-if="method === 'in-cluster'" class="cl-hint">{{ $t('cluster.dialog.inClusterHint') }}</p>
            <div v-else-if="method === 'file'" class="cl-pick">
              <button type="button" class="cl-btn" @click="chooseFile">
                {{ filePath ? $t('cluster.dialog.chooseOther') : $t('cluster.dialog.chooseFile') }}
              </button>
              <span v-if="filePath" class="cl-pick-path cl-mono" :title="filePath">{{ filePath }}</span>
              <span v-else class="cl-hint" style="margin: 0">{{ $t('cluster.dialog.noFile') }}</span>
            </div>
            <template v-else>
              <textarea v-model="text" class="cl-textarea cl-mono" spellcheck="false"
                placeholder="apiVersion: v1&#10;kind: Config&#10;…" :aria-label="$t('cluster.dialog.kubeconfigText')"
                @input="onTextInput"></textarea>
              <div v-if="!capabilities.fileDialog" class="cl-pick">
                <button type="button" class="cl-btn" @click="uploadFile">{{ $t('cluster.dialog.uploadFromFile') }}</button>
              </div>
              <p class="cl-hint">{{ $t('cluster.dialog.textHint') }}</p>
            </template>

            <p v-if="hasSource && probe.inspectError.value" class="cl-hint is-error">{{ probe.inspectError.value }}</p>
          </div>

          <ClusterConnectionFields v-if="probe.info.value" v-model:context="contextName" v-model:namespace="namespace"
            :info="probe.info.value" :namespace-valid="namespaceValid" :check="probe.check.value"
            :checking="probe.checking.value" :can-test="!probe.inspecting.value" @test="runTest" />

          <!-- Вход через провайдера (oidc, облачный плагин): личный у каждого или общий у команды -->
          <div v-if="shareable" class="cl-field" data-testid="shared-account">
            <label class="cl-check">
              <input v-model="sharedAccount" type="checkbox" data-testid="shared-account-checkbox" />
              {{ $t('cluster.dialog.sharedAccount') }}
            </label>
            <p class="cl-hint">{{ $t(sharedAccount ? 'cluster.dialog.sharedAccountOn' : 'cluster.dialog.sharedAccountOff') }}</p>
          </div>
        </section>

        <section class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionAppearance') }}</h3>
          <ClusterAppearanceFields v-model:icon="icon" v-model:color="color" v-model:environment="environment"
            :name="name" />
        </section>

        <div v-if="error" class="cl-error" role="alert">{{ error }}</div>
      </div>

      <footer class="cl-foot">
        <span class="cl-foot-spacer"></span>
        <button type="button" class="cl-btn" @click="emit('close')">{{ $t('common.cancel') }}</button>
        <button type="button" class="cl-btn cl-btn-primary" :disabled="!canSubmit" @click="submit">
          <span v-if="saving" class="cl-spin" aria-hidden="true"></span>
          {{ $t('cluster.dialog.submit') }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AddClusterPayload, ClusterSettings, KubeconfigSource } from '../types'
import { NO_KUBECONFIG_SOURCE, pickContextName, useKubeconfigProbe } from '../composables/useKubeconfigProbe'
import { useCapabilities } from '../composables/useCapabilities'
import { useTeams } from '../composables/useTeams'
import { canShareAccount } from '../composables/clusterAuth'
import { can } from '../composables/useAuth'
import { Perm } from '../types/auth'
import { pickTextFile } from '../platform/files'
import { errorText } from '../composables/errorText'
import ClusterAvatar from './cluster/ClusterAvatar.vue'
import ClusterAppearanceFields from './cluster/ClusterAppearanceFields.vue'
import ClusterConnectionFields from './cluster/ClusterConnectionFields.vue'
import { DEFAULT_CLUSTER_COLOR, MAX_CLUSTER_NAME, isValidNamespace } from './cluster/clusterStyle'

const props = defineProps<{
  pickFile: () => Promise<string>
  /** Возвращает текст ошибки или null, если кластер добавлен (диалог закрывает родитель) */
  submit: (payload: AddClusterPayload) => Promise<string | null>
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const uid = useId()
const probe = useKubeconfigProbe()

const name = ref('')
const icon = ref('')
const color = ref(DEFAULT_CLUSTER_COLOR)
const environment = ref('')
const namespace = ref('')
const contextName = ref('')

// Без нативного диалога (веб) kubeconfig передаётся текстом: вставкой или загрузкой файла браузером
const { capabilities } = useCapabilities()

// Сервисный режим: кластер принадлежит команде; выбирать можно только команды, где пользователь администратор
const { adminTeams, loadTeams } = useTeams()
const teamId = ref('')
onMounted(async () => {
  if (!capabilities.value.teams) return
  await loadTeams()
  if (!teamId.value && adminTeams.value.length) teamId.value = adminTeams.value[0].id
})
// «In-cluster» — кластер, в котором работает сам сервер: доступ с правами его ServiceAccount, поэтому только админу организации
const canInCluster = computed(() => capabilities.value.inCluster && can(Perm.UsersManage))
const method = ref<'file' | 'text' | 'in-cluster'>(capabilities.value.fileDialog ? 'file' : 'text')
const filePath = ref('')
const text = ref('')
const sharedAccount = ref(false)

const saving = ref(false)
const error = ref('')
const nameInput = ref<HTMLInputElement | null>(null)
const pressedOverlay = ref(false)

const hasSource = computed(() => {
  if (method.value === 'in-cluster') return false
  return method.value === 'file' ? !!filePath.value : !!text.value.trim()
})
const source = computed<KubeconfigSource>(() =>
  method.value === 'file'
    ? { ...NO_KUBECONFIG_SOURCE, path: filePath.value }
    : { ...NO_KUBECONFIG_SOURCE, text: text.value },
)

const namespaceValid = computed(() => isValidNamespace(namespace.value))
// Общую учётную запись можно выбрать, когда кластер идёт в команду и входит через провайдера
const shareable = computed(() => {
  if (method.value !== 'text' || !capabilities.value.teams || !teamId.value) return false
  const ctx = probe.info.value?.contexts.find(c => c.name === contextName.value)
  return canShareAccount(ctx?.auth, true)
})

const kubeconfigReady = computed(() =>
  hasSource.value && !probe.inspecting.value && !probe.inspectError.value && !!probe.info.value,
)

const canSubmit = computed(() =>
  !saving.value && (!capabilities.value.teams || !!teamId.value) && name.value.trim().length > 0 && namespaceValid.value &&
  (method.value === 'in-cluster' || kubeconfigReady.value),
)

const dirty = computed(() =>
  !!name.value || !!namespace.value || !!environment.value || hasSource.value,
)

const subtitle = computed(
  () => probe.info.value?.contexts.find(c => c.name === contextName.value)?.server || t('cluster.dialog.addSubtitle'),
)

async function inspectSource() {
  probe.resetCheck()
  if (!hasSource.value) {
    probe.reset()
    return
  }
  const info = await probe.inspect(source.value)
  if (info) contextName.value = pickContextName(info, contextName.value)
}

async function chooseFile() {
  const path = await props.pickFile()
  if (!path) return
  error.value = ''
  filePath.value = path
  await inspectSource()
}

async function uploadFile() {
  try {
    const content = await pickTextFile()
    if (content === null) return
    error.value = ''
    text.value = content
    await inspectSource()
  } catch (e) {
    error.value = errorText(e)
  }
}

function setMethod(m: 'file' | 'text' | 'in-cluster') {
  if (method.value === m) return
  method.value = m
  error.value = ''
  contextName.value = ''
  void inspectSource()
}

// Вставленный текст разбирается с задержкой — не на каждое нажатие клавиши
let inspectTimer: ReturnType<typeof setTimeout> | undefined
function onTextInput() {
  probe.resetCheck()
  clearTimeout(inspectTimer)
  inspectTimer = setTimeout(() => { void inspectSource() }, 450)
}

async function runTest() {
  await probe.test(source.value, contextName.value)
}

async function submit() {
  if (!canSubmit.value) return
  saving.value = true
  error.value = ''
  const settings: ClusterSettings = {
    displayName: name.value.trim(),
    icon: icon.value,
    color: color.value,
    environment: environment.value,
    defaultNamespace: namespace.value,
    contextName: contextName.value,
    argoUrl: '',
    sharedAccount: shareable.value && sharedAccount.value,
  }
  const teamOwner = capabilities.value.teams ? teamId.value : undefined
  let payload: AddClusterPayload
  if (method.value === 'in-cluster') payload = { kind: 'in-cluster', settings, teamId: teamId.value }
  else if (method.value === 'file') payload = { kind: 'file', path: filePath.value, settings }
  else {
    payload = { kind: 'text', text: text.value, settings, teamId: teamOwner }
  }
  const err = await props.submit(payload)
  saving.value = false
  if (err) error.value = err
}

// Закрытие по клику мимо диалога — только пока форма пуста и нажатие началось на подложке
function onOverlayClick() {
  const startedOnOverlay = pressedOverlay.value
  pressedOverlay.value = false
  if (startedOnOverlay && !dirty.value && !saving.value) emit('close')
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && !saving.value) {
    emit('close')
  } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    void submit()
  }
}

onMounted(async () => {
  window.addEventListener('keydown', onKeydown)
  await nextTick()
  nameInput.value?.focus()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  clearTimeout(inspectTimer)
})
</script>
