<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="br-overlay" @click.self="cancel" @keydown.esc="cancel">
    <div class="br-dialog" role="dialog" aria-modal="true" :aria-label="$t('bugReport.title')" data-testid="bug-report-dialog">
      <h3>{{ $t('bugReport.title') }}</h3>

      <p v-if="hint === 'mail'" class="br-hint" data-testid="bug-report-hint-mail">
        {{ $t('bugReport.hintMail', { recipient: report.status.value?.recipient ?? '' }) }}
      </p>
      <p v-else-if="hint === 'relay'" class="br-hint" data-testid="bug-report-hint-relay">{{ $t('bugReport.hintRelay') }}</p>
      <p v-else-if="hint === 'queue'" class="br-hint" data-testid="bug-report-hint-queue">{{ $t('bugReport.hintQueue') }}</p>
      <p v-else-if="report.status.value" class="br-warn" data-testid="bug-report-unconfigured">{{ $t('bugReport.notConfiguredAdmin') }}</p>

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

      <!-- Отчёты, которые не удалось доставить, ждут на этом компьютере. Архив — для машины, откуда связи нет совсем. -->
      <div v-if="pending > 0" class="br-pending" data-testid="bug-report-pending">
        <span>{{ $t('bugReport.pending', { n: pending }) }}</span>
        <button type="button" class="btn" :disabled="busy" data-testid="bug-report-export" @click="report.exportPending()">
          {{ $t('bugReport.exportArchive') }}
        </button>
      </div>
      <p v-if="report.exportedPath.value" class="br-ok" data-testid="bug-report-exported">
        {{ $t('bugReport.exported') }} <span class="br-path" :title="report.exportedPath.value">{{ report.exportedPath.value }}</span>
      </p>

      <!-- Почтовый сервер необязателен и есть только в десктопе; на сервере его задаёт администратор (KUBRON_BUGREPORT_SMTP_*) -->
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
        <button v-if="hint !== 'none'" type="button" class="btn btn-primary" :disabled="!canSend" data-testid="bug-report-send" @click="send">
          {{ busy ? $t('bugReport.sending') : $t('bugReport.send') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBugReport } from '../../composables/useBugReport'
import { useNotifications } from '../../composables/useNotifications'
import { MAX_DESCRIPTION, canSendReport, deliveryHint } from './bugReportView'
import BugReportMailSettings from './BugReportMailSettings.vue'

const { t } = useI18n()
const report = useBugReport()
const notifications = useNotifications()
const showMail = ref(false)

const busy = computed(() => report.phase.value === 'sending')
const length = computed(() => [...report.description.value].length)
const hint = computed(() => deliveryHint(report.status.value))
const pending = computed(() => report.status.value?.pending ?? 0)
const canEditMail = computed(() => !!report.status.value?.canSave)
const canSend = computed(() => canSendReport(report.description.value, report.status.value, busy.value))

async function send() {
  const result = await report.send()
  if (!result) return
  // «Сохранён» — не успех и не ошибка: отчёт не потерян и уйдёт сам, когда появится связь
  if (result.status === 'queued') notifications.notify(t('bugReport.queued'), 'info', 7000)
  else notifications.notifySuccess(t('bugReport.sent'))
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
.br-pending { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 12px; font-size: 13px; color: #cdd6f4; }
.br-path { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left; font-family: monospace; font-size: 12px; color: #a6adc8; }
.br-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
</style>
