// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { investigateAction } from '../investigateAction'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, flatNsCol, ageCol, controlledByProp, managedByProp } from './common'
import IngressRulesSection from '../../components/resource/sections/IngressRulesSection.vue'
import IngressMetricsSection from '../../components/resource/sections/IngressMetricsSection.vue'
import { t } from '../../i18n'

const classLink = (i: any) => (i.class ? { id: 'ingressclass', ns: '', name: i.class } : null)

export const ingress = defineResource<k8s.IngressInfo>({
  id: 'ingress',
  kind: 'Ingress',
  actions: [investigateAction('Ingress')],
  view: 'ingresses',
  title: 'Ingresses',
  namespaced: true,
  nav: { group: 'network', label: 'Ingresses' },
  autoRefresh: true,

  columns: [
    flatNameCol,
    { ...flatNsCol, link: (i: any) => i.namespace ? { id: 'namespace', ns: '', name: i.namespace } : null },
    { key: 'class', label: 'defs.columns.class', link: classLink },
    { key: 'address', label: 'defs.columns.loadBalancers', formatter: (i: any) => i.address || '—' },
    { key: 'hosts', label: 'defs.columns.hosts', formatter: (i: any) => [...new Set(i.hosts ?? [])].join(', ') || '—' },
    { key: 'paths', label: 'defs.columns.rules', formatter: (i: any) => i.paths || '—' },
    { key: 'tls', label: 'defs.columns.tls', formatter: (i: any) => (i.tls ? t('defs.values.true') : t('defs.values.false')) },
    ageCol,
  ],

  headSections: [IngressMetricsSection],
  properties: [
    { label: 'defs.props.ingressClass', value: (i: any) => i.class, link: classLink },
    { label: 'defs.columns.ports', value: (i: any) => i.ports },
    { label: 'defs.columns.loadBalancers', value: (i: any) => i.address },
    { label: 'defs.columns.hosts', value: (i: any) => [...new Set(i.hosts ?? [])].join(', ') },
    { label: 'defs.columns.tls', value: (i: any) => (i.tls ? t('defs.values.true') : t('defs.values.false')) },
  ],
  sections: [IngressRulesSection],

  api: {
    list: ns => callApp('GetIngresses', ns),
    remove: (ns, name) => callApp('DeleteResource', 'ingress', ns, name),
  },
})