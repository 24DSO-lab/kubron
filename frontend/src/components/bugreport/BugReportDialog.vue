<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="br-overlay" @click.self="cancel" @keydown.esc="cancel">
    <div class="br-dialog" role="dialog" aria-modal="true" :aria-label="$t('bugReport.title')" data-testid="bug-report-dialog">
      <h3>{{ isDraft ? $t('bugReport.draft.title') : $t('bugReport.title') }}</h3>

      <!-- Почтовая программа открыта: письмо готово, осталось вложить снимок и отправить -->
      <section v-if="isDraft && draft" class="br-draft" data-testid="bug-report-draft">
        <p>{{ $t('bugReport.draft.opened', { recipient: report.status.value?.recipient ?? '' }) }}</p>
        <p v-if="draft.screenshotPath" :class="draft.copied ? 'br-ok' : 'br-warn'" data-testid="bug-report-draft-paste">
          {{ draft.copied ? $t('bugReport.draft.pasteOk', { keys: shortcut }) : $t('bugReport.draft.pasteFailed') }}
        </p>
        <div v-if="draft.screenshotPath" class="br-file">
          <span class="br-path" :title="draft.screenshotPath" data-testid="bug-report-draft-path">{{ draft.screenshotPath }}</span>
          <button type="button" class="btn" data-testid="bug-report-draft-reveal" @click="report.revealFile(draft.screenshotPath)">
            {{ $t('bugReport.draft.reveal') }}
          </button>
        </div>
        <template v-if="draft.truncated && draft.textPath">
          <p class="br-warn" data-testid="bug-report-draft-truncated">{{ $t('bugReport.draft.truncated') }}</p>
          <div class="br-file">
            <span class="br-path" :title="draft.textPath">{{ draft.textPath }}</span>
            <button type="button" class="btn" @click="report.revealFile(draft.textPath)">{{ $t('bugReport.draft.reveal') }}</button>
          </div>
        </template>
        <p class="br-hint">{{ $t('bugReport.draft.nothingOpened') }}</p>
        <p v-if="report.error.value" class="br-error" role="alert">{{ report.error.value }}</p>
        <div class="br-actions">
          <button type="button" class="btn" data-testid="bug-report-draft-back" @click="report.backToReport()">
            {{ $t('bugReport.draft.back') }}
          </button>
          <button type="button" class="btn btn-primary" data-testid="bug-report-draft-done" @click="report.close()">
            {{ $t('bugReport.draft.done') }}
          </button>
        </div>
      </section>

      <template v-else>
      <p class="br-hint">{{ $t('bugReport.hint', { recipient: report.status.value?.recipient ?? '' }) }}</p>

      <section class="br-shot">
        <template v-if="report.screenshot.value">
          <img :src="report.screenshot.value" :alt="$t('bugReport.screenshotAlt')" class="br-preview"
            :class="{ off: !report.includeScreenshot.value }" data-testid="bug-report-preview" />
          <div class="br-shot-row">
            <label class="br-check">
              <input v-model="report.includeScreenshot.value" type="checkbox" :disabled="busy"
                data-testid="bug-report-include" />
              {{ $t('bugReport.attach') }}
            </label>
            <button type="button" class="btn" :disabled="busy" data-testid="bug-report-retake" @click="report.retake()">
              {{ $t('bugReport.retake') }}
            </button>
          </div>
          <p class="br-warn">{{ $t('bugReport.privacy') }}</p>
        </template>
        <template v-else>
          <p class="br-warn" data-testid="bug-report-no-shot">
            {{ report.captureFailed.value ? $t('bugReport.captureFailed') : $t('bugReport.noScreenshot') }}
          </p>
          <button type="button" class="btn" :disabled="busy" data-testid="bug-report-retake" @click="report.retake()">
            {{ $t('bugReport.retake') }}
          </button>
        </template>
      </section>

      <label class="br-field" for="br-description">{{ $t('bugReport.description') }}</label>
      <textarea id="br-description" v-model="report.description.value" class="br-text" rows="5" :maxlength="MAX_DESCRIPTION"
        :placeholder="$t('bugReport.descriptionPlaceholder')" :disabled="busy" data-testid="bug-report-description"></textarea>
      <div class="br-count">{{ length }} / {{ MAX_DESCRIPTION }}</div>

      <label class="br-field" for="br-contact">{{ $t('bugReport.contact') }}</label>
      <input id="br-contact" v-model="report.contact.value" type="email" class="br-input" autocomplete="email"
        spellcheck="false" :placeholder="$t('bugReport.contactPlaceholder')" :disabled="busy" data-testid="bug-report-contact" />

      <p v-if="canDraft && !configured" class="br-hint" data-testid="bug-report-mailclient-hint">{{ $t('bugReport.mailClientHint') }}</p>
      <p v-else-if="report.status.value && !configured" class="br-warn" data-testid="bug-report-unconfigured">
        {{ canEditMail ? $t('bugReport.notConfigured') : $t('bugReport.notConfiguredAdmin') }}
      </p>
      <!-- Почтой управляет пользователь только в десктопе; на сервере её задаёт администратор (KUBRON_BUGREPORT_SMTP_*) -->
      <template v-if="canEditMail">
        <button type="button" class="br-link" data-testid="bug-report-mail-toggle" @click="showMail = !showMail">
          {{ $t('bugReport.mailSettings') }}
        </button>
        <BugReportMailSettings v-if="showMail" @changed="report.refreshStatus()" />
      </template>

      <p v-if="report.error.value" class="br-error" role="alert" data-testid="bug-report-error">{{ report.error.value }}</p>

      <div class="br-actions">
        <button type="button" class="btn" :disabled="busy" data-testid="bug-report-cancel" @click="cancel">
          {{ $t('common.cancel') }}
        </button>
        <button v-if="canDraft" type="button" class="btn" :class="{ 'btn-primary': !configured }" :disabled="!canOpen"
          data-testid="bug-report-mailclient" @click="report.openInMailClient()">
          {{ $t('bugReport.openMailClient') }}
        </button>
        <button v-if="configured" type="button" class="btn btn-primary" :disabled="!canSend" data-testid="bug-report-send" @click="send">
          {{ busy ? $t('bugReport.sending') : $t('bugReport.send') }}
        </button>
      </div>
      </template>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBugReport } from '../../composables/useBugReport'
import { useNotifications } from '../../composables/useNotifications'
import { MAX_DESCRIPTION, canOpenMailClient, canSendReport, pasteShortcut } from './bugReportView'
import BugReportMailSettings from './BugReportMailSettings.vue'

const { t } = useI18n()
const report = useBugReport()
const notifications = useNotifications()
const showMail = ref(false)

const busy = computed(() => report.phase.value === 'sending')
const length = computed(() => [...report.description.value].length)
const isDraft = computed(() => report.phase.value === 'draft')
const draft = computed(() => report.draft.value)
const configured = computed(() => !!report.status.value?.configured)
const canDraft = computed(() => !!report.status.value?.canDraft)
const shortcut = computed(() => pasteShortcut(navigator.platform ?? ''))
const canOpen = computed(() => canOpenMailClient(report.description.value, report.status.value, busy.value))
const canEditMail = computed(() => !!report.status.value?.canSave)
const canSend = computed(() => canSendReport(report.description.value, report.status.value, busy.value))

async function send() {
  if (await report.send()) notifications.notifySuccess(t('bugReport.sent'))
}

function cancel() {
  if (!busy.value) report.close()
}
</script>

<style scoped>
.br-overlay {
  position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.55); padding: 16px;
}
.br-dialog {
  width: 100%; max-width: 560px; max-height: 100%; overflow-y: auto; padding: 22px; background: #181825;
  border: 1px solid #313244; border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5); text-align: left;
}
h3 { margin: 0 0 8px; color: #89b4fa; font-size: 17px; }
.br-hint { margin: 0 0 12px; color: #a6adc8; font-size: 13px; }
.br-shot { margin-bottom: 12px; }
.br-preview { display: block; width: 100%; max-height: 220px; object-fit: contain; background: #11111b; border: 1px solid #313244; border-radius: 8px; }
.br-preview.off { opacity: 0.35; }
.br-shot-row { display: flex; align-items: center; justify-content: space-between; margin-top: 8px; }
.br-check { display: flex; align-items: center; gap: 6px; font-size: 13px; color: #cdd6f4; }
.br-warn { margin: 8px 0 0; color: #f9e2af; font-size: 12px; }
.br-field { display: block; margin: 10px 0 4px; font-size: 13px; color: #cdd6f4; }
.br-text, .br-input {
  width: 100%; box-sizing: border-box; background: #1e1e2e; color: #cdd6f4; border: 1px solid #313244;
  border-radius: 8px; padding: 8px 10px; font: inherit; font-size: 13px;
}
.br-text { resize: vertical; min-height: 90px; }
.br-count { text-align: right; font-size: 11px; color: #6c7086; }
.br-link { background: none; border: none; padding: 0; margin: 10px 0 4px; color: #89b4fa; cursor: pointer; font-size: 12px; }
.br-error { margin: 10px 0 0; color: #f38ba8; font-size: 13px; }
.br-ok { margin: 8px 0 0; color: #a6e3a1; font-size: 13px; }
.br-draft p { margin: 8px 0 0; font-size: 13px; color: #cdd6f4; }
.br-file { display: flex; align-items: center; gap: 8px; margin-top: 8px; }
.br-path { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left; font-family: monospace; font-size: 12px; color: #a6adc8; }
.br-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
</style>
