// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, ageCol, controlledByProp, managedByProp } from './common'
import { badgeClass } from '../status'
import { podsByClassTab } from '../podsByClass'
import { t } from '../../i18n'

const yesNo = (v: boolean) => (v ? t('defs.values.true') : t('defs.values.false'))

export const priorityClass = defineResource<k8s.PriorityClassInfo>({
  id: 'priorityclass',
  kind: 'PriorityClass',
  view: 'priorityclasses',
  title: 'PriorityClasses',
  namespaced: false,
  nav: { group: 'config', label: 'Priority Classes' },

  autoRefresh: true,

  columns: [
    flatNameCol,
    { key: 'value', label: 'defs.columns.value' },
    { key: 'globalDefault', label: 'defs.columns.globalDefault', formatter: (p: any) => yesNo(p.globalDefault), colorFn: (p: any) => (p.globalDefault ? 'var(--green)' : 'var(--text-gray)') },
    ageCol,
  ],

  properties: [
    { label: 'defs.columns.value', value: (p: any) => p.value },
    { label: 'defs.columns.globalDefault', value: (p: any) => yesNo(p.globalDefault), badge: (p: any) => (p.globalDefault ? badgeClass('Active') : 'rd-unk') },
    { label: 'defs.props.preemptionPolicy', value: (p: any) => p.preemptionPolicy },
    { label: 'defs.props.description', value: (p: any) => p.description },
    { label: 'defs.props.finalizers', value: (p: any) => (p.finalizers ?? []).join(', ') },
    controlledByProp,
    managedByProp,
  ],

  tabs: [
    { id: 'pods', label: 'defs.tabs.pods', component: podsByClassTab('priorityClassName') },
  ],

  api: {
    list: () => callApp('GetPriorityClasses'),
    remove: (_ns, name) => callApp('DeleteResource', 'priorityclass', '', name),
  },
})
