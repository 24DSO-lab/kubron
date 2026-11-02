<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="cl-overlay" @mousedown.self="pressedOverlay = true" @click.self="onOverlayClick">
    <div class="cl-dialog" role="dialog" aria-modal="true" :aria-labelledby="`${uid}-title`">
      <header class="cl-head">
        <ClusterAvatar :name="name" :icon="icon" :color="color" :environment="environment" :size="44" show-env />
        <div class="cl-head-text">
          <div :id="`${uid}-title`" class="cl-title">{{ name.trim() || $t('cluster.dialog.untitled') }}</div>
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
              autocomplete="off" spellcheck="false" @keydown.enter.prevent="submit" />
          </div>
        </section>

        <section class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionArgo') }}</h3>
          <div class="cl-field">
            <label :for="`${uid}-argo-url`" class="cl-label">{{ $t('cluster.dialog.argoUrl') }}</label>
            <input :id="`${uid}-argo-url`" v-model="argoUrl" class="cl-input" :maxlength="MAX_ARGO_URL"
              :placeholder="$t('cluster.dialog.argoUrlPlaceholder')" autocomplete="off" spellcheck="false"
              :aria-invalid="!argoUrlValid" @keydown.enter.prevent="submit" />
            <p class="cl-hint" :class="{ 'is-error': !argoUrlValid }">
              {{ argoUrlValid ? $t('cluster.dialog.argoUrlHint') : $t('cluster.dialog.argoUrlInvalid') }}
            </p>
          </div>
        </section>

        <section class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionAppearance') }}</h3>
          <ClusterAppearanceFields v-model:icon="icon" v-model:color="color" v-model:environment="environment"
            :name="name" />
        </section>

        <section class="cl-section">
          <h3 class="cl-section-title">{{ $t('cluster.dialog.sectionConnection') }}</h3>

          <div class="cl-field">
            <span class="cl-label">{{ $t('cluster.edit.kubeconfig') }}</span>
            <div class="cl-kc">
              <div class="cl-kc-info">
                <div class="cl-kc-name cl-mono" :title="kcLabel">{{ kcLabel }}</div>
                <div class="cl-kc-state" :class="{ 'is-changed': hasReplacement }">{{ kcState }}</div>
              </div>
              <div class="cl-kc-actions">
                <button type="button" class="cl-btn cl-btn-sm" @click="replaceWithFile">
                  {{ capabilities.fileDialog ? $t('cluster.edit.replaceWithFile') : $t('cluster.dialog.uploadFromFile') }}
                </button>
                <button v-if="kcMode !== 'text'" type="button" class="cl-btn cl-btn-sm" @click="editText">
                  {{ $t('cluster.edit.editText') }}
                </button>
                <button v-if="kcMode !== 'keep'" type="button" class="cl-btn cl-btn-sm cl-btn-quiet"
                  @click="revertKubeconfig">
                  {{ $t('cluster.edit.revert') }}
                </button>
              </div>
            </div>

            <template v-if="kcMode === 'text'">
              <textarea ref="kcTextarea" v-model="kcText" class="cl-textarea cl-mono" spellcheck="false"
                :aria-label="$t('cluster.dialog.kubeconfigText')" style="margin-top: 8px" @input="onTextInput"></textarea>
              <p v-if="secretsHidden" class="cl-hint" data-testid="secret-hidden">
                {{ $t('cluster.edit.secretHidden') }}
                <button type="button" class="cl-btn cl-btn-sm" data-testid="secret-replace" @click="replaceSecret">
                  {{ $t('cluster.edit.replaceSecret') }}
                </button>
              </p>
              <p v-else class="cl-hint is-warn">{{ $t('cluster.edit.textWarning') }}</p>
            </template>
            <p v-if="hasReplacement && probe.inspectError.value" class="cl-hint is-error">
              {{ probe.inspectError.value }}
            </p>
          </div>

          <ClusterConnectionFields v-model:context="contextName" v-model:namespace="namespace"
            :info="probe.info.value" :namespace-valid="namespaceValid" :check="probe.check.value"
            :checking="probe.checking.value" :can-test="canTest" @test="runTest" />
        </section>

        <!-- oidc-password: сервер входит в провайдер входа сам; сменился пароль — введите новый -->
        <section v-if="cluster.mode === 'oidc-password'" class="cl-section" data-testid="oidc-update">
          <h3 class="cl-section-title">{{ $t('cluster.edit.oidcTitle') }}</h3>
          <div class="cl-field">
            <p class="cl-hint">{{ $t('cluster.edit.oidcHint') }}</p>
            <label :for="`${uid}-oidc-user`" class="cl-label">{{ $t('cluster.dialog.oidcUser') }}</label>
            <input :id="`${uid}-oidc-user`" v-model="oidcUser" class="cl-input" autocomplete="off" spellcheck="false" />
            <label :for="`${uid}-oidc-pass`" class="cl-label">{{ $t('cluster.dialog.oidcPassword') }}</label>
            <input :id="`${uid}-oidc-pass`" v-model="oidcPassword" type="password" class="cl-input" autocomplete="new-password" />
            <div class="cl-pick">
              <button type="button" class="cl-btn" :disabled="!oidcReady || oidcBusy" @click="updateOidc">
                {{ $t('cluster.edit.oidcUpdate') }}
              </button>
            </div>
            <p v-if="oidcMessage" class="cl-hint" :class="{ 'is-error': oidcFailed }" role="status">{{ oidcMessage }}</p>
          </div>
        </section>

        <div v-if="error" class="cl-error" role="alert">{{ error }}</div>
      </div>

      <footer class="cl-foot">
        <button type="button" class="cl-btn cl-btn-danger" :disabled="saving" @click="emit('delete', cluster.id)">
          {{ $t('cluster.edit.deleteCluster') }}
        </button>
        <span class="cl-foot-spacer"></span>
        <button type="button" class="cl-btn" @click="emit('close')">{{ $t('common.cancel') }}</button>
        <button type="button" class="cl-btn cl-btn-primary" :disabled="!canSave" @click="submit">
          <span v-if="saving" class="cl-spin" aria-hidden="true"></span>
          {{ $t('common.save') }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ClusterSettings, ClusterStatus, KubeconfigSource } from '../../types'
import { isValidArgoUrl, MAX_ARGO_URL } from '../../composables/argoUrl'
import { NO_KUBECONFIG_SOURCE, pickContextName, useKubeconfigProbe } from '../../composables/useKubeconfigProbe'
import { errorText } from '../../composables/errorText'
import { firstPlaceholderRange, hasHiddenSecret } from '../../composables/kubeconfigSecrets'
import ClusterAvatar from './ClusterAvatar.vue'
import ClusterAppearanceFields from './ClusterAppearanceFields.vue'
import ClusterConnectionFields from './ClusterConnectionFields.vue'
import { MAX_CLUSTER_NAME, findGlyph, isValidNamespace } from './clusterStyle'
import { callApp } from '../../platform/api'
import { pickTextFile } from '../../platform/files'
import { useCapabilities } from '../../composables/useCapabilities'

// Правка кластера: название, оформление, окружение и подключение (kubeconfig, контекст,
// namespace по умолчанию). Закрывает диалог родитель — после успешного сохранения.
const props = defineProps<{
  cluster: ClusterStatus
  pickFile: () => Promise<string>
  /** Возвращает текст ошибки или null, если сохранено */
  save: (id: string, settings: ClusterSettings, kubeconfig: KubeconfigSource) => Promise<string | null>
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'delete', id: string): void
}>()

const { t } = useI18n()
const uid = useId()
const probe = useKubeconfigProbe()

// Старый эмодзи-значок при первой правке заменяется монограммой
const initial = {
  name: props.cluster.displayName,
  icon: findGlyph(props.cluster.icon)?.key ?? '',
  color: props.cluster.color,
  environment: props.cluster.environment || '',
  namespace: props.cluster.defaultNamespace || '',
  context: props.cluster.contextName,
  argoUrl: props.cluster.argoUrl || '',
}

const name = ref(initial.name)
const icon = ref(initial.icon)
const color = ref(initial.color)
const environment = ref(initial.environment)
const namespace = ref(initial.namespace)
const contextName = ref(initial.context)
const argoUrl = ref(initial.argoUrl)

type KubeconfigMode = 'keep' | 'file' | 'text'
const { capabilities } = useCapabilities()
const kcMode = ref<KubeconfigMode>('keep')
const kcPath = ref('')
const kcText = ref('')
const kcOriginalText = ref('')
const kcTextarea = ref<HTMLTextAreaElement | null>(null)

// Сервер не отдаёт значения секретов (сервисный режим): на их месте плейсхолдер, при сохранении сервер подставит прежние
const secretsHidden = computed(() => hasHiddenSecret(kcText.value))

// Выделяет первый плейсхолдер: вводимое значение заменит его
function replaceSecret() {
  const el = kcTextarea.value
  const range = firstPlaceholderRange(kcText.value)
  if (!el || !range) return
  el.focus()
  el.setSelectionRange(range[0], range[1])
}

// Смена логина и пароля oidc-password: действие отдельное от «Сохранить», пароль в форме не задерживается
const oidcUser = ref('')
const oidcPassword = ref('')
const oidcBusy = ref(false)
const oidcMessage = ref('')
const oidcFailed = ref(false)
const oidcReady = computed(() => oidcUser.value.trim().length > 0 && oidcPassword.value.length > 0)

async function updateOidc() {
  if (!oidcReady.value || oidcBusy.value) return
  oidcBusy.value = true
  oidcMessage.value = ''
  try {
    await callApp('SetClusterOIDCCredentials', props.cluster.id, { username: oidcUser.value.trim(), password: oidcPassword.value })
    oidcFailed.value = false
    oidcMessage.value = t('cluster.edit.oidcUpdated')
    oidcPassword.value = ''
  } catch (e) {
    oidcFailed.value = true
    oidcMessage.value = errorText(e)
  } finally {
    oidcBusy.value = false
  }
}

const saving = ref(false)
const error = ref('')
const nameInput = ref<HTMLInputElement | null>(null)
const pressedOverlay = ref(false)

const textDirty = computed(() => kcMode.value === 'text' && kcText.value !== kcOriginalText.value)
const hasReplacement = computed(() => (kcMode.value === 'file' && !!kcPath.value) || textDirty.value)

// Что отправить на сохранение: пустой источник означает «kubeconfig не менять»
const replacement = computed<KubeconfigSource>(() => {
  if (kcMode.value === 'file' && kcPath.value) return { ...NO_KUBECONFIG_SOURCE, path: kcPath.value }
  if (textDirty.value) return { ...NO_KUBECONFIG_SOURCE, text: kcText.value, baseClusterId: props.cluster.id }
  return { ...NO_KUBECONFIG_SOURCE }
})

// Что разбирать и проверять: будущий kubeconfig, а без замены — сохранённый
const probeSource = computed<KubeconfigSource>(() =>
  hasReplacement.value ? replacement.value : { ...NO_KUBECONFIG_SOURCE, clusterId: props.cluster.id },
)

const nameValid = computed(() => name.value.trim().length > 0)
const namespaceValid = computed(() => isValidNamespace(namespace.value))
const argoUrlValid = computed(() => isValidArgoUrl(argoUrl.value))
const canTest = computed(() => !!probe.info.value && !probe.inspecting.value)
const canSave = computed(() =>
  !saving.value && nameValid.value && namespaceValid.value && argoUrlValid.value &&
  !(hasReplacement.value && (probe.inspecting.value || !!probe.inspectError.value)),
)

const dirty = computed(() =>
  name.value !== initial.name || icon.value !== initial.icon || color.value !== initial.color ||
  environment.value !== initial.environment || namespace.value !== initial.namespace ||
  contextName.value !== initial.context || argoUrl.value.trim() !== initial.argoUrl || hasReplacement.value,
)

const selectedServer = computed(() => probe.info.value?.contexts.find(c => c.name === contextName.value)?.server ?? '')
const subtitle = computed(() => selectedServer.value || props.cluster.kubeconfigFile)

const kcLabel = computed(() => {
  if (kcMode.value === 'file' && kcPath.value) return kcPath.value
  return props.cluster.kubeconfigFile
})
const kcState = computed(() => {
  if (kcMode.value === 'file' && kcPath.value) return t('cluster.edit.stateFile')
  if (textDirty.value) return t('cluster.edit.stateDirty')
  if (kcMode.value === 'text') return t('cluster.edit.stateText')
  return t('cluster.edit.stateKeep')
})

async function refreshProbe() {
  const info = await probe.inspect(probeSource.value)
  if (info) contextName.value = pickContextName(info, contextName.value)
}

async function replaceWithFile() {
  if (!capabilities.value.fileDialog) return replaceWithUpload()
  const path = await props.pickFile()
  if (!path) return
  error.value = ''
  kcMode.value = 'file'
  kcPath.value = path
  probe.resetCheck()
  await refreshProbe()
}

// Веб: файл читает браузер, на сервер уходит текст kubeconfig
async function replaceWithUpload() {
  try {
    const content = await pickTextFile()
    if (content === null) return
    error.value = ''
    kcOriginalText.value = ''
    kcText.value = content
    kcPath.value = ''
    kcMode.value = 'text'
    probe.resetCheck()
    await refreshProbe()
  } catch (e) {
    error.value = errorText(e)
  }
}

async function editText() {
  error.value = ''
  try {
    const text = await callApp('GetClusterKubeconfig', props.cluster.id)
    kcOriginalText.value = text
    kcText.value = text
    kcPath.value = ''
    kcMode.value = 'text'
  } catch (e) {
    error.value = errorText(e)
  }
}

async function revertKubeconfig() {
  kcMode.value = 'keep'
  kcPath.value = ''
  kcText.value = ''
  kcOriginalText.value = ''
  probe.resetCheck()
  await refreshProbe()
}

// Текст разбирается с задержкой — не на каждое нажатие клавиши
let inspectTimer: ReturnType<typeof setTimeout> | undefined
function onTextInput() {
  probe.resetCheck()
  clearTimeout(inspectTimer)
  inspectTimer = setTimeout(() => { void refreshProbe() }, 450)
}

async function runTest() {
  await probe.test(probeSource.value, contextName.value)
}

async function submit() {
  if (!canSave.value) return
  saving.value = true
  error.value = ''
  const settings: ClusterSettings = {
    displayName: name.value.trim(),
    icon: icon.value,
    color: color.value,
    environment: environment.value,
    defaultNamespace: namespace.value,
    contextName: contextName.value,
    argoUrl: argoUrl.value.trim(),
  }
  const err = await props.save(props.cluster.id, settings, replacement.value)
  saving.value = false
  if (err) error.value = err
}

// Закрытие по клику мимо диалога — только если ничего не изменено и нажатие началось на подложке:
// иначе неосторожный клик (или выделение мышью, вышедшее за край) стоил бы введённого текста
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
  nameInput.value?.select()
  await refreshProbe()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  clearTimeout(inspectTimer)
})
</script>
