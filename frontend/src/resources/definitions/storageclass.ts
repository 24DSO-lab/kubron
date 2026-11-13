// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, ageCol, controlledByProp, managedByProp } from './common'
import StorageClassParametersTab from '../../components/resource/sections/StorageClassParametersTab.vue'
import StorageClassVolumesTab from '../../components/resource/sections/StorageClassVolumesTab.vue'
import { t } from '../../i18n'

const yesNo = (v: boolean) => (v ? t('common.yes') : t('common.no'))

export const storageClass = defineResource<k8s.StorageClassInfo>({
  id: 'storageclass',
  kind: 'StorageClass',
  view: 'storageclasses',
  title: 'StorageClasses',
  icon: 'storage-class',
  namespaced: false,
  nav: { group: 'storage', label: 'Storage Classes' },
  autoRefresh: true,

  columns: [
    flatNameCol,
    { key: 'provisioner', label: 'defs.columns.provisioner' },
    { key: 'reclaimPolicy', label: 'defs.columns.reclaimPolicy' },
    { key: 'volumeBindingMode', label: 'defs.columns.bindingMode' },
    {
      key: 'allowVolumeExpansion',
      label: 'defs.columns.expansion',
      formatter: s => yesNo(s.allowVolumeExpansion),
    },
    {
      key: 'default',
      label: 'defs.columns.default',
      formatter: s => yesNo(s.default),
      colorFn: s => (s.default ? 'var(--green)' : 'var(--text-gray)'),
    },
    { key: 'volumes', label: 'defs.columns.pvs', formatter: s => String(s.volumes?.length ?? 0) },
    ageCol,
  ],

  // Name / Age / Created выводит шапка панели; Labels и Annotations — общие секции
  properties: [
    { label: 'defs.columns.provisioner', value: s => s.provisioner },
    { label: 'defs.props.volumeBindingMode', value: s => s.volumeBindingMode },
    { label: 'defs.columns.reclaimPolicy', value: s => s.reclaimPolicy },
    controlledByProp,
    managedByProp,
  ],
  tabs: [
    { id: 'parameters', label: 'defs.tabs.parameters', component: StorageClassParametersTab },
    { id: 'volumes', label: 'defs.tabs.persistentVolumes', component: StorageClassVolumesTab },
  ],

  api: {
    list: () => callApp('GetStorageClasses'),
    remove: (_ns, name) => callApp('DeleteResource', 'storageclass', '', name),
  },
})