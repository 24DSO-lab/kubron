<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="ch-overlay" data-testid="challenge-overlay" @keydown.esc.stop.prevent="emit('cancel')">
    <div ref="dialogEl" class="ch-dialog" role="dialog" aria-modal="true" tabindex="-1" data-testid="challenge-dialog"
      :data-kind="challenge.kind" :aria-labelledby="titleId" :aria-describedby="descId" :aria-busy="busy"
      >
      <h3 :id="titleId" class="ch-title">{{ $t(`credential.${challenge.kind}.title`) }}</h3>
      <p :id="descId" class="ch-desc">{{ $t(`credential.${challenge.kind}.description`) }}</p>

      <!-- Вывод плагина и подсказки: только текст -->
      <pre v-if="challenge.message" class="ch-message" data-testid="challenge-message">{{ challenge.message }}</pre>

      <p v-if="error" class="ch-error" role="alert" data-testid="challenge-error">{{ error }}</p>
      <p v-if="expired" class="ch-error" role="alert" data-testid="challenge-expired">{{ $t('credential.common.expired') }}</p>

      <!-- device-code -->
      <template v-if="challenge.kind === 'device-code'">
        <div class="ch-code-row">
          <code class="ch-code" data-testid="device-code" :aria-label="$t('credential.field.userCode')">{{ challenge.userCode }}</code>
          <button type="button" class="ch-btn" data-testid="copy-code" @click="copy(challenge.userCode ?? '', 'code')">
            {{ copied === 'code' ? $t('credential.common.copied') : $t('credential.common.copy') }}
          </button>
        </div>
        <a v-if="href" class="ch-link" :href="href" target="_blank" rel="noopener noreferrer" data-testid="challenge-link"
          @click.prevent="emit('open-url', href)">{{ $t('credential.common.openLink') }}</a>
      </template>

      <!-- browser-loopback, browser-callback -->
      <template v-else-if="challenge.kind === 'browser-loopback' || challenge.kind === 'browser-callback'">
        <button v-if="href" type="button" class="ch-btn" data-testid="open-again" @click="emit('open-url', href)">
          {{ $t('credential.common.openAgain') }}
        </button>
      </template>

      <!-- confirm-exec -->
      <template v-else-if="challenge.kind === 'confirm-exec' && challenge.exec">
        <dl class="ch-exec" data-testid="exec-details">
          <dt>{{ $t('credential.field.command') }}</dt>
          <dd><code data-testid="exec-command">{{ execLine }}</code></dd>
          <dt>{{ $t('credential.field.args') }}</dt>
          <dd>
            <ol v-if="args.length" class="ch-args" data-testid="exec-args">
              <li v-for="(a, i) in args" :key="i"><code>{{ a }}</code></li>
            </ol>
            <span v-else>{{ $t('credential.common.none') }}</span>
          </dd>
          <dt>{{ $t('credential.field.envNames') }}</dt>
          <dd>
            <span v-if="envNames.length" data-testid="exec-env"><code v-for="n in envNames" :key="n" class="ch-env">{{ n }}</code></span>
            <span v-else>{{ $t('credential.common.none') }}</span>
          </dd>
        </dl>
      </template>

      <!-- manual-token: команда только для показа, никогда не выполняется -->
      <template v-else-if="challenge.kind === 'manual-token'">
        <div class="ch-cmd">
          <pre class="ch-pre" data-testid="manual-command" tabindex="0" :aria-label="$t('credential.field.command')">{{ cmdLine }}</pre>
          <button type="button" class="ch-btn" data-testid="copy-command" @click="copy(cmdLine, 'command')">
            {{ copied === 'command' ? $t('credential.common.copied') : $t('credential.common.copy') }}
          </button>
        </div>
      </template>

      <!-- paste-redirect: ссылка входа до формы -->
      <template v-else-if="challenge.kind === 'paste-redirect'">
        <a v-if="href" class="ch-link" :href="href" target="_blank" rel="noopener noreferrer" data-testid="challenge-link"
          @click.prevent="emit('open-url', href)">{{ $t('credential.common.openLink') }}</a>
        <p class="ch-hint" data-testid="paste-hint">{{ $t('credential.paste-redirect.hint') }}</p>
      </template>

      <!-- Формы -->
      <form v-if="formKind" class="ch-form" novalidate data-testid="challenge-form" @submit.prevent="onSubmit">
        <div v-for="(f, i) in fields" :key="f.name" class="ch-field">
          <label :for="`${uid}-${f.name}`" class="ch-label">{{ fieldLabel(f.name) }}</label>
          <div class="ch-input-row">
            <textarea v-if="challenge.kind === 'manual-token' || challenge.kind === 'paste-redirect' || isMultilineField(challenge.kind, f.name)"
              :id="`${uid}-${f.name}`" v-model="values[f.name]" class="ch-input ch-area" :class="{ 'ch-masked': f.secret && !reveal[f.name] }"
              :rows="isMultilineField(challenge.kind, f.name) ? 8 : 3" spellcheck="false" autocomplete="off" autocapitalize="off" wrap="off" :disabled="busy"
              :data-testid="`field-${f.name}`" :data-autofocus="i === 0 ? '' : undefined" />
            <input v-else :id="`${uid}-${f.name}`" v-model="values[f.name]" class="ch-input"
              :type="f.secret && !reveal[f.name] ? 'password' : 'text'" spellcheck="false" autocapitalize="off"
              :autocomplete="autocompleteFor(f.name)" :disabled="busy" :data-testid="`field-${f.name}`"
              :data-autofocus="i === 0 ? '' : undefined" />
            <button v-if="f.secret" type="button" class="ch-btn ch-toggle" :aria-pressed="!!reveal[f.name]"
              :data-testid="`toggle-${f.name}`" :disabled="busy" @click="reveal[f.name] = !reveal[f.name]">
              {{ reveal[f.name] ? $t('credential.common.hide') : $t('credential.common.show') }}
            </button>
          </div>
        </div>
        <div class="ch-actions">
          <button type="submit" class="ch-btn ch-primary" data-testid="challenge-submit" :disabled="busy || !canSubmit">
            {{ busy ? $t('credential.common.sending') : $t(`credential.${challenge.kind}.submit`) }}
          </button>
          <button type="button" class="ch-btn" data-testid="challenge-cancel" @click="emit('cancel')">{{ $t('common.cancel') }}</button>
        </div>
      </form>

      <!-- confirm-exec: три решения -->
      <div v-else-if="challenge.kind === 'confirm-exec'" class="ch-actions">
        <button type="button" class="ch-btn ch-primary" data-testid="decision-once" :disabled="busy" data-autofocus
          @click="emit('submit', { decision: 'once' })">{{ $t('credential.confirm-exec.once') }}</button>
        <button type="button" class="ch-btn" data-testid="decision-always" :disabled="busy"
          @click="emit('submit', { decision: 'always' })">{{ $t('credential.confirm-exec.always') }}</button>
        <button type="button" class="ch-btn" data-testid="challenge-cancel" @click="emit('cancel')">{{ $t('common.cancel') }}</button>
      </div>

      <!-- Ожидание: код устройства, браузер -->
      <div v-else class="ch-wait">
        <p class="ch-status" role="status" aria-live="polite" data-testid="challenge-waiting">
          <span class="ch-spinner" aria-hidden="true"></span>{{ $t('credential.common.waiting') }}
        </p>
        <p v-if="!expired" class="ch-timer" role="timer" data-testid="challenge-countdown">
          {{ $t('credential.common.expiresIn', { time: countdown }) }}
        </p>
        <div class="ch-actions">
          <button v-if="expired" type="button" class="ch-btn ch-primary" data-testid="challenge-restart" @click="emit('restart')">
            {{ $t('credential.common.tryAgain') }}
          </button>
          <button type="button" class="ch-btn" data-testid="challenge-cancel" @click="emit('cancel')">{{ $t('common.cancel') }}</button>
        </div>
      </div>

      <p v-if="formKind && expired" class="ch-actions">
        <button type="button" class="ch-btn ch-primary" data-testid="challenge-restart" @click="emit('restart')">{{ $t('credential.common.tryAgain') }}</button>
      </p>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Challenge, ChallengeValues } from '../../types/credential'
import {
  challengeFields, clearFieldValues, commandLine, execCommandLine, fieldsFilled, formatCountdown, hasForm, isMultilineField, safeHref,
} from './challengeView'

const props = defineProps<{
  challenge: Challenge
  busy: boolean
  error: string
  /** Растёт после отклонённого ответа: секретные поля очищаются, фокус возвращается в первое поле */
  attempt: number
  remainingSec: number
  expired: boolean
}>()
const emit = defineEmits<{
  (e: 'submit', values: ChallengeValues): void
  (e: 'cancel'): void
  (e: 'restart'): void
  (e: 'open-url', url: string): void
}>()

const { t, te } = useI18n()
const uid = `ch-${useId()}`
const titleId = `${uid}-title`
const descId = `${uid}-desc`
const dialogEl = ref<HTMLElement | null>(null)
const values = reactive<Record<string, string>>({})
const reveal = reactive<Record<string, boolean>>({})
const copied = ref('')

const fields = computed(() => challengeFields(props.challenge))
const formKind = computed(() => hasForm(props.challenge.kind))
const canSubmit = computed(() => fieldsFilled(fields.value, values) && !props.expired)
const href = computed(() => safeHref(props.challenge.url))
const cmdLine = computed(() => commandLine(props.challenge.command))
const execLine = computed(() => (props.challenge.exec ? execCommandLine(props.challenge.exec) : ''))
const args = computed(() => props.challenge.exec?.args ?? [])
const envNames = computed(() => Object.keys(props.challenge.exec?.env ?? {}).sort())
const countdown = computed(() => formatCountdown(props.remainingSec))

/** Подпись поля: credential.field.<name>, а для неизвестного провайдерного поля — его имя как есть */
function fieldLabel(name: string): string {
  const key = `credential.field.${name}`
  return te(key) ? t(key) : name
}

function autocompleteFor(name: string): string {
  if (name === 'username') return 'username'
  if (name === 'password') return 'current-password'
  return 'off'
}

function resetValues(onlySecrets: boolean) {
  clearFieldValues(fields.value, values, onlySecrets)
  for (const f of fields.value) reveal[f.name] = false
}

function onSubmit() {
  if (props.busy || !canSubmit.value) return
  const out: ChallengeValues = {}
  for (const f of fields.value) out[f.name] = f.secret ? (values[f.name] ?? '') : (values[f.name] ?? '').trim()
  emit('submit', out)
}

async function copy(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = what
    setTimeout(() => { if (copied.value === what) copied.value = '' }, 2000)
  } catch {
    copied.value = ''
  }
}

function focusFirst() {
  const el = dialogEl.value?.querySelector<HTMLElement>('[data-autofocus]') ?? dialogEl.value
  el?.focus()
}

/** Фокус не выходит за пределы диалога */
function trapTab(ev: KeyboardEvent) {
  const root = dialogEl.value
  if (!root) return
  const items = Array.from(root.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), textarea:not(:disabled), pre[tabindex]'))
    .filter((e) => e.getClientRects().length > 0 && e.getAttribute('aria-hidden') !== 'true')
  if (items.length === 0) { ev.preventDefault(); root.focus(); return }
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement
  if (!root.contains(active)) { ev.preventDefault(); (ev.shiftKey ? last : first).focus() }
  else if (ev.shiftKey && (active === first || active === root)) { ev.preventDefault(); last.focus() }
  else if (!ev.shiftKey && active === last) { ev.preventDefault(); first.focus() }
}

function onDocumentKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Tab') trapTab(ev)
}

/** Страховка на случай, если браузер или другой обработчик всё же увёл фокус наружу. */
function onDocumentFocusin(ev: FocusEvent) {
  const root = dialogEl.value
  if (root && ev.target instanceof Node && !root.contains(ev.target)) focusFirst()
}

watch(() => props.challenge.id, () => { resetValues(false); void nextTick(focusFirst) })
watch(() => props.attempt, () => { resetValues(true); void nextTick(focusFirst) })
onMounted(() => {
  resetValues(false)
  focusFirst()
  document.addEventListener('keydown', onDocumentKeydown, true)
  document.addEventListener('focusin', onDocumentFocusin, true)
})
onBeforeUnmount(() => {
  resetValues(true) // секреты не переживают диалог
  document.removeEventListener('keydown', onDocumentKeydown, true)
  document.removeEventListener('focusin', onDocumentFocusin, true)
})
</script>

<style scoped>
.ch-overlay { position: fixed; inset: 0; z-index: 1100; display: flex; align-items: center; justify-content: center; background: rgba(0, 0, 0, 0.55); }
.ch-dialog { width: 100%; max-width: 460px; max-height: 90vh; overflow: auto; padding: 24px; background: var(--bg-sunken); color: var(--text); border: 1px solid var(--surface-0); border-radius: 14px; box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5); outline: none; }
.ch-title { margin: 0 0 8px; color: var(--accent); font-size: 17px; }
.ch-desc, .ch-hint { margin: 0 0 14px; font-size: 13px; color: var(--text-muted); line-height: 1.45; }
.ch-error { margin: 0 0 12px; padding: 8px 10px; font-size: 13px; color: var(--danger); background: rgba(var(--danger-rgb), 0.1); border-radius: 8px; }
.ch-message, .ch-pre { margin: 0 0 12px; padding: 8px 10px; max-height: 140px; overflow: auto; white-space: pre-wrap; word-break: break-all; font-size: 12px; background: var(--bg-crust); border-radius: 8px; }
.ch-code-row, .ch-cmd, .ch-input-row { display: flex; gap: 8px; align-items: flex-start; }
.ch-cmd .ch-pre { flex: 1; }
.ch-code { flex: 1; padding: 12px; text-align: center; font-size: 26px; letter-spacing: 4px; font-weight: 700; background: var(--bg-crust); border-radius: 10px; user-select: all; }
.ch-link { display: inline-block; margin: 12px 0; color: var(--accent); }
.ch-form { display: flex; flex-direction: column; gap: 12px; }
.ch-label { display: block; margin-bottom: 4px; font-size: 12px; color: var(--text-muted); }
.ch-input { flex: 1; width: 100%; box-sizing: border-box; padding: 8px 10px; color: var(--text); background: var(--bg-crust); border: 1px solid var(--surface-1); border-radius: 8px; font: inherit; }
.ch-input:focus-visible, .ch-btn:focus-visible, .ch-link:focus-visible, .ch-pre:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.ch-area { resize: vertical; font-family: monospace; font-size: 12px; }
.ch-masked { -webkit-text-security: disc; }
.ch-exec { margin: 0 0 14px; font-size: 13px; }
.ch-exec dt { margin-top: 8px; color: var(--text-muted); font-size: 12px; }
.ch-exec dd { margin: 2px 0 0; word-break: break-all; }
.ch-args { margin: 0; padding-left: 20px; }
.ch-env { margin-right: 6px; padding: 1px 6px; background: var(--bg-crust); border-radius: 6px; }
.ch-actions { display: flex; gap: 8px; flex-wrap: wrap; margin: 14px 0 0; }
.ch-btn { padding: 8px 14px; color: var(--text); background: var(--surface-0); border: 1px solid var(--surface-1); border-radius: 8px; cursor: pointer; font: inherit; }
.ch-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.ch-primary { color: var(--bg-crust); background: var(--accent); border-color: var(--accent); }
.ch-toggle { padding: 8px 10px; }
.ch-status { display: flex; align-items: center; gap: 8px; margin: 12px 0 4px; font-size: 13px; }
.ch-timer { margin: 0; font-size: 12px; color: var(--text-muted); }
.ch-spinner { width: 14px; height: 14px; border: 2px solid var(--surface-1); border-top-color: var(--accent); border-radius: 50%; animation: ch-spin 0.9s linear infinite; }
@keyframes ch-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .ch-spinner { animation: none; } }
</style>
