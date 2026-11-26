<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="bd-overlay" @mousedown.self="pressed = true" @click.self="onOverlay">
    <div class="bd-dialog" role="dialog" aria-modal="true" aria-labelledby="bd-title">
      <header class="bd-head">
        <h3 id="bd-title">{{ mode === 'new' ? $t('baseline.create.title') : $t('baseline.version_dialog.title') }}</h3>
        <button type="button" class="bd-close" :aria-label="$t('common.close')" :disabled="busy" @click="emit('close')">✕</button>
      </header>

      <div class="bd-body">
        <template v-if="mode === 'new'">
          <label class="bd-field">{{ $t('baseline.create.name') }}
            <input v-model.trim="name" class="form-input" maxlength="120" autocomplete="off" spellcheck="false" />
          </label>
          <label class="bd-field">{{ $t('baseline.create.namespaces') }}
            <input v-model="namespacesText" class="form-input" autocomplete="off" spellcheck="false" :placeholder="$t('baseline.create.namespacesHint')" @input="invalidate" />
          </label>
          <label class="bd-field">{{ $t('baseline.create.selector') }}
            <input v-model.trim="selector" class="form-input" autocomplete="off" spellcheck="false" :placeholder="SELECTOR_EXAMPLE" @input="invalidate" />
          </label>
          <fieldset class="bd-field bd-kinds" :aria-label="$t('baseline.create.kinds')">
            <legend>{{ $t('baseline.create.kinds') }}</legend>
            <label v-for="k in store.kinds.value" :key="k" class="bd-check">
              <input v-model="pickedKinds" type="checkbox" :value="k" @change="invalidate" /> {{ k }}
            </label>
            <span v-if="!pickedKinds.length" class="bd-muted">{{ $t('baseline.create.kindsAll') }}</span>
          </fieldset>
        </template>

        <div class="bd-row">
          <button class="btn" type="button" :disabled="busy || (mode === 'new' && !ready)" @click="runPreview">
            {{ previewing ? $t('baseline.create.previewing') : $t('baseline.create.preview') }}
          </button>
          <span v-if="preview" class="bd-muted">{{ $t('baseline.create.expires', { time: formatTime(preview.expiresAt) }) }}</span>
        </div>

        <section v-if="preview" class="bd-preview" aria-live="polite">
          <p v-if="previewEmpty(preview)" class="bd-warn">{{ $t('baseline.create.nothing') }}</p>
          <p v-else>{{ $t('baseline.create.found', { objects: preview.objects, hashed: preview.hashed }) }}</p>

          <template v-if="mode === 'version' && preview.hasPrevious">
            <p v-if="preview.rulesChanged" class="bd-warn">{{ $t('baseline.version_dialog.rulesChanged') }}</p>
            <ul v-else class="bd-diff">
              <li>{{ $t('baseline.version_dialog.added', { count: preview.added?.length ?? 0 }) }}</li>
              <li>{{ $t('baseline.version_dialog.removed', { count: preview.removed?.length ?? 0 }) }}</li>
              <li>{{ $t('baseline.version_dialog.changed', { count: preview.changed?.length ?? 0 }) }}</li>
              <li>{{ $t('baseline.version_dialog.unchanged', { count: preview.unchanged }) }}</li>
            </ul>
            <p class="bd-muted">{{ $t('baseline.version_dialog.note') }}</p>
          </template>

          <div v-if="preview.skipped?.length" class="bd-skipped">
            <strong>{{ $t('baseline.create.skipped') }}</strong>
            <ul>
              <li v-for="(s, i) in preview.skipped" :key="i">{{ s.ref.kind }}{{ s.ref.name ? ` ${s.ref.namespace ? s.ref.namespace + '/' : ''}${s.ref.name}` : s.ref.namespace ? ` (${s.ref.namespace})` : '' }} — {{ $t(`baseline.reason.${s.reason}`) }}</li>
            </ul>
          </div>

          <template v-if="!previewEmpty(preview)">
            <label class="bd-field">{{ $t('baseline.create.comment') }}
              <textarea v-model.trim="comment" class="form-input" rows="2" maxlength="2000" />
            </label>
            <label class="bd-check bd-confirm">
              <input v-model="confirmed" type="checkbox" /> {{ $t('baseline.create.confirm') }}
            </label>
          </template>
        </section>
        <p v-else-if="stale" class="bd-warn">{{ $t('baseline.create.stale') }}</p>

        <p v-if="store.error.value" class="form-error" role="alert">{{ store.error.value }}</p>
      </div>

      <footer class="bd-foot">
        <button class="btn" type="button" :disabled="busy" @click="emit('close')">{{ $t('baseline.create.cancel') }}</button>
        <button class="btn btn-primary" type="button" :disabled="!canSubmit" @click="submit">
          {{ mode === 'new' ? $t('baseline.create.submit') : $t('baseline.version_dialog.submit') }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { formatTime } from '../../i18n/format'
import { emptySpec, parseList, previewEmpty, specReady, type BaselineStore } from '../../composables/useBaseline'
import type { BaselinePreview, BaselineSpec } from '../../types/baseline'

/** Пример селектора меток: синтаксис Kubernetes, не переводится */
const SELECTOR_EXAMPLE = 'app=web'

const props = defineProps<{ store: BaselineStore; mode: 'new' | 'version' }>()
const emit = defineEmits<{ close: []; done: [] }>()

const name = ref('')
const namespacesText = ref('')
const selector = ref('')
const pickedKinds = ref<string[]>([])
const comment = ref('')
const confirmed = ref(false)
const preview = ref<BaselinePreview | null>(null)
const previewing = ref(false)
const submitting = ref(false)
const stale = ref(false)
const pressed = ref(false)

const busy = computed(() => previewing.value || submitting.value)

const spec = computed<BaselineSpec>(() => ({
  ...emptySpec(), namespaces: parseList(namespacesText.value), selector: selector.value, kinds: pickedKinds.value,
}))
const ready = computed(() => specReady(spec.value))
const canSubmit = computed(() =>
  !busy.value && !!preview.value && !previewEmpty(preview.value) && confirmed.value && comment.value.trim() !== ''
  && (props.mode === 'version' || name.value !== ''))

onMounted(() => { if (props.mode === 'new') void props.store.loadKinds() })

/** Любое изменение выборки обесценивает предпросмотр и подтверждение: подтверждается только увиденное */
function invalidate() {
  preview.value = null
  confirmed.value = false
}

async function runPreview() {
  previewing.value = true
  stale.value = false
  confirmed.value = false
  const p = props.mode === 'new' ? await props.store.previewNew(spec.value) : await props.store.previewVersion()
  previewing.value = false
  preview.value = p ?? null
}

async function submit() {
  if (!preview.value) return
  submitting.value = true
  const ok = props.mode === 'new'
    ? await props.store.create(name.value, spec.value, comment.value, preview.value.token, confirmed.value)
    : await props.store.addVersion(comment.value, preview.value.token, confirmed.value)
  submitting.value = false
  if (ok) { emit('done'); return }
  // Токен мог устареть (кластер изменился): нужен новый предпросмотр и новое подтверждение
  stale.value = true
  invalidate()
}

function onOverlay() {
  if (pressed.value && !busy.value) emit('close')
  pressed.value = false
}
</script>

<style scoped>
.bd-overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55); display: flex; align-items: center; justify-content: center; z-index: 60; padding: 16px; }
.bd-dialog { background: #1e1e2e; border: 1px solid #313244; border-radius: 10px; width: min(640px, 100%); max-height: 90vh; display: flex; flex-direction: column; }
.bd-head { display: flex; justify-content: space-between; align-items: center; padding: 14px 18px; border-bottom: 1px solid #313244; }
.bd-head h3 { margin: 0; font-size: 16px; }
.bd-close { background: none; border: 0; color: #a6adc8; cursor: pointer; font-size: 16px; }
.bd-body { padding: 16px 18px; overflow: auto; display: flex; flex-direction: column; gap: 12px; }
.bd-field { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: #a6adc8; border: 0; padding: 0; margin: 0; }
.bd-kinds { flex-direction: row; flex-wrap: wrap; gap: 6px 14px; align-items: center; }
.bd-kinds legend { padding: 0; margin-bottom: 4px; }
.bd-check { display: flex; gap: 6px; align-items: center; font-size: 13px; color: #cdd6f4; }
.bd-confirm { align-items: flex-start; }
.bd-row { display: flex; gap: 12px; align-items: center; }
.bd-muted { color: #6c7086; font-size: 12px; }
.bd-warn { color: #f9e2af; margin: 0; }
.bd-preview { background: #181825; border-radius: 8px; padding: 12px 14px; display: flex; flex-direction: column; gap: 10px; font-size: 13px; }
.bd-preview p { margin: 0; }
.bd-diff { margin: 0; padding-left: 18px; }
.bd-skipped ul { margin: 4px 0 0; padding-left: 18px; color: #a6adc8; }
.bd-foot { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 18px; border-top: 1px solid #313244; }
</style>
