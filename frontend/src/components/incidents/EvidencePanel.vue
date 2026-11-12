<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="evidence" data-testid="evidence-panel">
    <p class="note" data-testid="never-external">{{ $t('incidents.evidence.neverExternal') }}</p>

    <label class="capture">
      <input type="checkbox" :checked="incident.saveLogs" :disabled="busy" data-testid="capture-toggle" @change="toggleCapture" />
      {{ $t('incidents.evidence.captureOn') }}
    </label>

    <p v-if="fragments.some((f) => f.contentHidden)" class="note hidden" data-testid="content-hidden">
      {{ $t('incidents.evidence.hidden') }}
    </p>

    <ul class="list">
      <li v-for="f in fragments" :key="f.id" class="frag" data-testid="fragment" :data-hidden="f.contentHidden">
        <div class="head">
          {{ $t('incidents.evidence.container', { container: f.container, pod: f.pod,
            instance: $t(f.previous ? 'incidents.evidence.instance.previous' : 'incidents.evidence.instance.current') }) }}
          <span class="badge">{{ f.auto ? $t('incidents.evidence.auto') : $t('incidents.evidence.manual') }}</span>
          <span v-if="f.truncated" class="badge">{{ $t('incidents.evidence.truncated') }}</span>
        </div>
        <div class="meta" data-testid="fragment-meta">
          {{ $t('incidents.evidence.meta', { start: f.startLine, end: f.endLine, anchor: f.anchorLine, lines: f.lines, bytes: f.bytes,
            replacements: f.replacements, version: f.redactorVersion }) }}
        </div>
        <div class="actions">
          <button v-if="!f.contentHidden && !shown[f.id]" class="btn btn-secondary" type="button" data-testid="fragment-show" @click="show(f.id)">
            {{ $t('incidents.evidence.show') }}
          </button>
          <button v-if="shown[f.id]" class="btn btn-secondary" type="button" data-testid="fragment-hide" @click="hide(f.id)">
            {{ $t('incidents.evidence.hide') }}
          </button>
          <button v-if="canDelete" class="btn btn-danger" type="button" :disabled="busy" data-testid="fragment-delete" @click="emit('delete', f.id)">
            {{ $t('incidents.evidence.delete') }}
          </button>
        </div>
        <pre v-if="shown[f.id] != null" class="content" data-testid="fragment-content">{{ shown[f.id] }}</pre>
        <p v-if="errors[f.id]" class="form-error" data-testid="fragment-error">{{ errors[f.id] }}</p>
      </li>
    </ul>

    <h4 class="sub">{{ $t('incidents.sections.evidence') }}</h4>
    <ul class="list">
      <li v-for="e in notSaved" :key="`${e.pod}/${e.container}/${e.previous}`" class="frag unsaved" data-testid="evidence-missing">
        <div class="head">
          {{ $t('incidents.evidence.container', { container: e.container, pod: e.pod,
            instance: $t(e.previous ? 'incidents.evidence.instance.previous' : 'incidents.evidence.instance.current') }) }}
        </div>
        <div class="meta">{{ $t('incidents.evidence.notSaved', { reason: reasonText(e.reason) }) }}</div>
        <div class="actions">
          <button class="btn btn-secondary" type="button" :disabled="busy || !incident.saveLogs" data-testid="evidence-save" @click="emit('save', e)">
            {{ $t('incidents.evidence.save') }}
          </button>
        </div>
      </li>
    </ul>

    <button v-if="canDelete && fragments.length" class="btn btn-danger" type="button" :disabled="busy" data-testid="delete-all" @click="emit('deleteAll')">
      {{ $t('incidents.evidence.deleteAll') }}
    </button>
  </div>
</template>

<script lang="ts" setup>
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorText } from '../../composables/errorText'
import { reasonText } from '../../composables/incidentModel'
import type { Evidence, Fragment, FragmentView, Incident } from '../../types/incident'

const props = defineProps<{
  incident: Incident
  fragments: FragmentView[]
  /** Прочитать содержимое: сервер проверяет право diagnostics.logs при каждом вызове */
  read: (id: string) => Promise<Fragment>
  canDelete: boolean
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'save', ev: Evidence): void
  (e: 'delete', id: string): void
  (e: 'deleteAll'): void
  (e: 'capture', enabled: boolean): void
}>()

const shown = reactive<Record<string, string | null>>({})
const errors = reactive<Record<string, string>>({})
const { t } = useI18n()

const notSaved = computed(() => props.incident.evidence.filter((e) => e.status === 'not_saved'))

async function show(id: string) {
  delete errors[id]
  try {
    shown[id] = (await props.read(id)).text
  } catch (e) {
    errors[id] = t('incidents.evidence.loadFailed', { error: errorText(e) })
  }
}

function hide(id: string) {
  delete shown[id] // текст не остаётся в памяти интерфейса после скрытия
}

function toggleCapture(ev: Event) {
  emit('capture', (ev.target as HTMLInputElement).checked)
}
</script>

<style scoped>
.evidence { display: flex; flex-direction: column; gap: 10px; }
.note { margin: 0; font-size: 12px; color: #a6adc8; }
.note.hidden { color: #f9e2af; }
.capture { font-size: 13px; color: #cdd6f4; display: flex; gap: 8px; align-items: center; }
.list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.frag { padding: 10px 12px; border-radius: 8px; background: #1e1e2e; border: 1px solid #313244; font-size: 13px; }
.frag.unsaved { border-style: dashed; }
.head { color: #cdd6f4; }
.badge { margin-left: 6px; padding: 1px 6px; border-radius: 4px; background: #313244; color: #a6adc8; font-size: 11px; }
.meta { margin-top: 4px; color: #a6adc8; font-size: 12px; }
.actions { margin-top: 8px; display: flex; gap: 8px; }
.content { margin: 8px 0 0; padding: 10px; max-height: 320px; overflow: auto; background: #11111b; border-radius: 6px; font-size: 12px; white-space: pre-wrap; word-break: break-word; }
.sub { margin: 8px 0 0; font-size: 13px; color: #a6adc8; }
</style>
