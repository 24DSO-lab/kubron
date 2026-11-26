<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div v-if="dialogOpen" class="ud-overlay" @click.self="closeDialog" @keydown.esc="closeDialog">
    <div class="ud-dialog" role="dialog" aria-modal="true" :aria-label="$t('update.title')" data-testid="update-dialog"
      tabindex="-1" ref="dialogEl">
      <div class="ud-head">
        <h3>{{ $t('update.title') }}</h3>
        <button type="button" class="ud-close" :aria-label="$t('common.close')" @click="closeDialog">✕</button>
      </div>

      <dl class="ud-facts">
        <dt>{{ $t('update.current') }}</dt>
        <dd data-testid="update-current">{{ displayVersion(version?.version) }}</dd>
        <template v-if="info">
          <dt>{{ $t('update.installedAs') }}</dt>
          <dd data-testid="update-method">{{ methodLabel }}</dd>
          <template v-if="info.latest">
            <dt>{{ $t('update.latest') }}</dt>
            <dd>{{ displayVersion(info.latest) }}</dd>
          </template>
          <template v-if="info.checkedAt">
            <dt>{{ $t('update.checkedAt') }}</dt>
            <dd>{{ formatTime(info.checkedAt) }}</dd>
          </template>
        </template>
      </dl>

      <template v-if="!info">
        <p class="ud-note">{{ $t('update.adminOnly') }}</p>
      </template>

      <template v-else>
        <p v-if="info.locked" class="ud-note">{{ $t('update.locked') }}</p>
        <p v-else-if="info.current === 'dev'" class="ud-note">{{ $t('update.devBuild') }}</p>
        <p v-else-if="info.available" class="ud-ok" data-testid="update-available">
          {{ $t('update.available', { version: displayVersion(info.latest) }) }}
        </p>
        <p v-else-if="info.latest" class="ud-note">{{ $t('update.upToDate') }}</p>
        <p v-else-if="!info.error" class="ud-note">{{ $t('update.notChecked') }}</p>

        <p v-if="errorCode" class="ud-error" role="alert">{{ errorText }}</p>

        <section v-if="info.available" class="ud-how">
          <h4>{{ $t('update.how') }}</h4>
          <p v-if="info.install.method === 'unknown'" class="ud-note">{{ $t('update.unknownMethod') }}</p>
          <p v-else class="ud-note">{{ $t(`update.hint.${hintKey}`) }}</p>

          <div v-for="c in info.commands" :key="c.id" class="ud-cmd">
            <span class="ud-cmd-label">{{ commandLabel(c.id) }}</span>
            <div class="ud-cmd-row">
              <code>{{ c.text }}</code>
              <button type="button" class="ud-copy" @click="copy(c.text)">{{ $t('update.copy') }}</button>
            </div>
          </div>

          <div v-if="info.download" class="ud-cmd">
            <span class="ud-cmd-label">{{ $t('update.download') }}</span>
            <div class="ud-cmd-row">
              <a :href="info.download.url" target="_blank" rel="noopener noreferrer">{{ info.download.name }}</a>
              <span v-if="info.download.size" class="ud-size">{{ formatSize(info.download.size) }}</span>
              <a v-if="info.download.checksumsUrl" :href="info.download.checksumsUrl" target="_blank"
                rel="noopener noreferrer" class="ud-sums">SHA256SUMS</a>
            </div>
          </div>

          <a v-if="info.releaseUrl" :href="info.releaseUrl" target="_blank" rel="noopener noreferrer"
            class="ud-release">{{ $t('update.releaseNotes') }}</a>
        </section>

        <label v-if="canToggle" class="ud-auto">
          <input type="checkbox" :checked="info.autoCheck" :disabled="toggling" @change="onToggle" />
          {{ $t('update.autoCheck') }}
        </label>
      </template>

      <div class="ud-actions">
        <button v-if="info && !info.locked && info.current !== 'dev'" type="button" class="ud-btn" :disabled="checking"
          data-testid="update-check" @click="checkNow">
          {{ checking ? $t('update.checking') : $t('update.check') }}
        </button>
        <button type="button" class="ud-btn ud-btn-secondary" @click="closeDialog">{{ $t('common.close') }}</button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { displayVersion, useUpdates } from '../../composables/useUpdates'
import { useNotifications } from '../../composables/useNotifications'
import { errorText as toErrorText } from '../../composables/errorText'

const { t, te, locale } = useI18n()
const notifications = useNotifications()
const { version, info, checking, failure, dialogOpen, closeDialog, checkNow, setAutoCheck } = useUpdates()
const dialogEl = ref<HTMLElement | null>(null)
const toggling = ref(false)

watch(dialogOpen, async open => {
  if (open) {
    await nextTick()
    dialogEl.value?.focus()
  }
})

/** Ключ локали для способа установки: «macos-app» → «macosApp» (дефис в пути ключа vue-i18n не используем) */
const camel = (m: string) => m.replace(/-(\w)/g, (_, c: string) => c.toUpperCase())
const method = computed(() => camel(info.value?.install.method ?? 'unknown'))
const methodLabel = computed(() => (te(`update.method.${method.value}`) ? t(`update.method.${method.value}`) : method.value))

/** Подсказка к способу установки: те же способы, что в Go (internal/update.Method); остальное — unknown. */
const hintKey = computed(() => (te(`update.hint.${method.value}`) ? method.value : 'unknown'))

/** Только десктоп: на сервере проверку задаёт администратор развёртывания (KUBRON_UPDATE_CHECK) */
const canToggle = computed(() => !!info.value && info.value.install.target === 'desktop' && !info.value.locked)

const errorCode = computed(() => info.value?.error || failure.value)
const errorText = computed(() => {
  const code = info.value?.error
  if (code) return te(`update.error.${code}`) ? t(`update.error.${code}`) : t('update.error.generic')
  return toErrorText(failure.value)
})

function commandLabel(id: string): string {
  return te(`update.command.${id}`) ? t(`update.command.${id}`) : id
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(locale.value)
}

function formatSize(bytes: number): string {
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    notifications.notifySuccess(t('update.copied'))
  } catch {
    notifications.notifyError(t('update.copyFailed'))
  }
}

async function onToggle(e: Event) {
  const checked = (e.target as HTMLInputElement).checked
  toggling.value = true
  try {
    await setAutoCheck(checked)
  } catch (err) {
    ;(e.target as HTMLInputElement).checked = !checked
    notifications.notifyError(toErrorText(err))
  } finally {
    toggling.value = false
  }
}
</script>

<style scoped>
.ud-overlay {
  position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
.ud-dialog {
  width: 100%; max-width: 560px; max-height: 90vh; overflow: auto; padding: 24px; text-align: left; outline: none;
  background: var(--bg-sunken); border: 1px solid var(--surface-0); border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5);
  color: var(--text); font-size: 13px;
}
.ud-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
h3 { margin: 0; color: var(--accent); font-size: 17px; }
h4 { margin: 16px 0 6px; font-size: 13px; color: var(--text); }
.ud-close { background: none; border: none; color: var(--text-muted); cursor: pointer; font-size: 14px; }
.ud-facts { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 0 0 12px; }
.ud-facts dt { color: var(--text-muted); }
.ud-facts dd { margin: 0; font-weight: 600; }
.ud-note { color: var(--text-muted); margin: 8px 0; }
.ud-ok { color: var(--ok); font-weight: 600; margin: 8px 0; }
.ud-error { color: var(--danger); margin: 8px 0; }
.ud-cmd { margin: 10px 0; }
.ud-cmd-label { display: block; color: var(--text-muted); font-size: 12px; margin-bottom: 4px; }
.ud-cmd-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ud-cmd-row code {
  flex: 1; min-width: 0; overflow-x: auto; white-space: nowrap; padding: 6px 8px;
  background: var(--bg-crust); border: 1px solid var(--surface-0); border-radius: 6px; font-size: 12px;
}
.ud-copy, .ud-btn {
  background: var(--accent); color: var(--bg); border: none; border-radius: 6px; padding: 6px 12px;
  font-size: 12px; font-weight: 600; cursor: pointer;
}
.ud-btn-secondary { background: var(--surface-0); color: var(--text); }
.ud-btn:disabled { opacity: 0.6; cursor: default; }
.ud-size, .ud-sums { color: var(--text-muted); font-size: 12px; }
a { color: var(--accent); }
.ud-release { display: inline-block; margin-top: 8px; }
.ud-auto { display: flex; align-items: center; gap: 8px; margin: 16px 0 0; color: var(--text); }
.ud-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
</style>
