<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <section class="settings" data-testid="incident-settings">
    <h3>{{ $t('incidents.settings.title') }}</h3>
    <p class="sub">{{ $t('incidents.settings.subtitle') }}</p>
    <p class="sub" data-testid="limits">
      {{ $t('incidents.settings.limits', { lines: info.limits.fragmentLines, bytes: info.limits.fragmentBytes / 1024,
        line: info.limits.lineBytes / 1024, count: info.limits.fragmentsPerIncident, total: info.limits.incidentFragmentBytes / 1024 }) }}
    </p>
    <p v-if="!info.canManage" class="sub" data-testid="settings-readonly">{{ $t('incidents.settings.readonly') }}</p>
    <form class="form" @submit.prevent="submit">
      <label class="check">
        <input v-model="draft.disableLogEvidence" type="checkbox" :disabled="!info.canManage" data-testid="setting-disable-logs" />
        {{ $t('incidents.settings.disableLogs') }}
      </label>
      <label class="field">
        <span>{{ $t('incidents.settings.excluded') }}</span>
        <textarea v-model="namespacesText" class="form-input" rows="3" :disabled="!info.canManage" data-testid="setting-excluded"></textarea>
      </label>
      <label class="field">
        <span>{{ $t('incidents.settings.fragmentDays', { max: info.limits.maxFragmentRetentionDays }) }}</span>
        <input v-model.number="draft.fragmentRetentionDays" class="form-input" type="number" min="1" :max="info.limits.maxFragmentRetentionDays"
          :disabled="!info.canManage" data-testid="setting-fragment-days" />
      </label>
      <label class="field">
        <span>{{ $t('incidents.settings.incidentDays', { max: info.limits.maxIncidentRetentionDays }) }}</span>
        <input v-model.number="draft.incidentRetentionDays" class="form-input" type="number" min="1" :max="info.limits.maxIncidentRetentionDays"
          :disabled="!info.canManage" data-testid="setting-incident-days" />
      </label>
      <p v-if="error" class="form-error" data-testid="settings-error">{{ error }}</p>
      <button v-if="info.canManage" class="btn" type="submit" :disabled="busy" data-testid="settings-save">{{ $t('incidents.settings.save') }}</button>
    </form>
  </section>
</template>

<script lang="ts" setup>
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { parseNamespaces, validateSettings } from '../../composables/incidentModel'
import type { IncidentSettings, IncidentSettingsInfo } from '../../types/incident'

const props = defineProps<{ info: IncidentSettingsInfo; busy?: boolean }>()
const emit = defineEmits<{ (e: 'save', s: IncidentSettings): void }>()
const { t } = useI18n()

const draft = reactive<IncidentSettings>({ ...props.info.settings })
const namespacesText = ref((props.info.settings.excludedNamespaces ?? []).join('\n'))
const error = ref('')

watch(() => props.info.settings, (s) => {
  Object.assign(draft, s)
  namespacesText.value = (s.excludedNamespaces ?? []).join('\n')
})

function submit() {
  const next: IncidentSettings = { ...draft, excludedNamespaces: parseNamespaces(namespacesText.value) }
  const bad = validateSettings(next, props.info.limits.maxFragmentRetentionDays, props.info.limits.maxIncidentRetentionDays)
  if (bad === 'fragmentDays') error.value = t('incidents.settings.fragmentDays', { max: props.info.limits.maxFragmentRetentionDays })
  else if (bad === 'incidentDays') error.value = t('incidents.settings.incidentDays', { max: props.info.limits.maxIncidentRetentionDays })
  else {
    error.value = ''
    emit('save', next)
  }
}
</script>

<style scoped>
.settings h3 { margin: 0 0 4px; font-size: 16px; color: #cdd6f4; }
.sub { margin: 2px 0 8px; font-size: 12px; color: #a6adc8; }
.form { display: flex; flex-direction: column; gap: 10px; max-width: 520px; }
.field { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: #a6adc8; }
.check { display: flex; gap: 8px; align-items: center; font-size: 13px; color: #cdd6f4; }
.form-input { padding: 8px 10px; }
</style>
