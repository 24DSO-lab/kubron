// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, ageCol, controlledByProp, managedByProp } from './common'
import { badgeClass } from '../status'
import IngressClassUsedBySection from '../../components/resource/sections/IngressClassUsedBySection.vue'
import { Perm } from '../../types/auth'
import { t } from '../../i18n'

const yesNo = (v: boolean) => (v ? t('defs.values.true') : t('defs.values.false'))

export const ingressClass = defineResource<k8s.IngressClassInfo>({
  id: 'ingressclass',
  kind: 'IngressClass',
  view: 'ingressclasses',
  title: 'IngressClasses',
  namespaced: false,
  nav: { group: 'network', label: 'Ingress Classes' },
  autoRefresh: true,

  columns: [
    flatNameCol,
    { key: 'controller', label: 'defs.columns.controller' },
    { key: 'paramsKind', label: 'defs.columns.parameters', formatter: (c: any) => (c.paramsKind ? `${c.paramsKind}/${c.paramsName}` : '—') },
    { key: 'default', label: 'defs.columns.default', formatter: (c: any) => yesNo(c.default), colorFn: (c: any) => (c.default ? 'var(--green)' : 'var(--text-gray)') },
    {
      key: 'usedBy',
      label: 'defs.columns.usedBy',
      formatter: (c: any) => (c.usedBy ?? []).map((u: any) => u.name).join(', ') || '—',
    },
    ageCol,
  ],

  properties: [
    { label: 'defs.columns.controller', value: (c: any) => c.controller },
    { label: 'defs.columns.default', value: (c: any) => yesNo(c.default), badge: (c: any) => (c.default ? badgeClass('Active') : 'rd-unk') },
    { label: 'defs.props.parametersKind', value: (c: any) => c.paramsKind },
    { label: 'defs.props.parametersName', value: (c: any) => c.paramsName },
    { label: 'defs.props.parametersApiGroup', value: (c: any) => c.paramsApiGroup },
    { label: 'defs.props.parametersScope', value: (c: any) => c.paramsScope },
    { label: 'defs.props.parametersNamespace', value: (c: any) => c.paramsNamespace },
    controlledByProp,
    managedByProp,
  ],
  sections: [IngressClassUsedBySection],
    actions: [
    {
      id: 'setdefault',
      label: 'defs.menu.setDefault',
      icon: 'star',
      permission: Perm.Write,
      when: (c: any) => !c.default,
      run: (c: any, ctx) =>
        ctx.perform(
          () => callApp('SetDefaultIngressClass', c.name, true),
          t('defs.ingressClass.nowDefault', { name: c.name }),
        ),
    },
    {
      id: 'unsetdefault',
      label: 'defs.menu.unsetDefault',
      icon: 'star',
      permission: Perm.Write,
      when: (c: any) => !!c.default,
      run: (c: any, ctx) =>
        ctx.perform(
          () => callApp('SetDefaultIngressClass', c.name, false),
          t('defs.ingressClass.noLongerDefault', { name: c.name }),
        ),
    },
  ],
  api: {
    list: () => callApp('GetIngressClasses'),
    remove: (_ns, name) => callApp('DeleteResource', 'ingressclass', '', name),
  },
})