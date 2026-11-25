// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { investigateAction } from '../investigateAction'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, flatNsCol, ageCol, controlledByProp, managedByProp } from './common'
import { formatMap } from '../utils'
import { statusColor } from '../status'
import ConnectionTab from '../../components/resource/sections/ConnectionTab.vue'
import EndpointSlicesTab from '../../components/resource/sections/EndpointSlicesTab.vue'
import EndpointsTab from '../../components/resource/sections/EndpointsTab.vue'
import ServiceNetworkPoliciesTab from '../../components/resource/sections/ServiceNetworkPoliciesTab.vue'

export const service = defineResource<k8s.ServiceInfo, k8s.ServiceDetail>({
  id: 'service',
  kind: 'Service',
  actions: [investigateAction('Service')],
  view: 'services',
  title: 'Services',
  namespaced: true,
  nav: { group: 'network', label: 'Services' },

  autoRefresh: true,

  columns: [
    flatNameCol,
    { ...flatNsCol, link: (s: any) => s.namespace ? { id: 'namespace', ns: '', name: s.namespace } : null },
    { key: 'type', label: 'defs.columns.type' },
    { key: 'clusterIP', label: 'defs.columns.clusterIp' },
    { key: 'ports', label: 'defs.columns.ports' },
    { key: 'externalIP', label: 'defs.columns.externalIp' },
    { key: 'selector', label: 'defs.columns.selector', formatter: (s: any) => formatMap(s.selector) },
    ageCol,
    { key: 'status', label: 'common.status', colorFn: (s: any) => statusColor(s.status) },
  ],

  properties: [
    { label: 'defs.props.sessionAffinity', value: s => s.sessionAffinity },
    { label: 'defs.columns.type', value: s => s.type },
    { label: 'defs.columns.selector', value: s => formatMap(s.selector) },
    { label: 'defs.props.finalizers', value: s => s.finalizers?.join(', ') },
    { label: 'defs.props.externalName', value: (s: any) => s.externalName },
    { label: 'defs.props.loadBalancer', value: (s: any) => (s.loadBalancer ?? []).join(', ') },
    { label: 'defs.props.externalTrafficPolicy', value: (s: any) => s.externalTrafficPolicy },
    { label: 'defs.props.internalTrafficPolicy', value: (s: any) => s.internalTrafficPolicy },
    
    controlledByProp,
    managedByProp,
  ],
  sections: [],
  tabs: [
    { id: 'connection', label: 'defs.tabs.connection', component: ConnectionTab },
    { id: 'endpoints', label: 'defs.tabs.endpoints', component: EndpointsTab },
    { id: 'endpointSlices', label: 'defs.tabs.endpointSlices', component: EndpointSlicesTab },
    { id: 'networkPolicies', label: 'defs.tabs.networkPolicies', component: ServiceNetworkPoliciesTab },
  ],

  api: {
    list: ns => callApp('GetServices', ns),
    get: (ns, name) => callApp('GetServiceDetail', ns, name),
    remove: (ns, name) => callApp('DeleteResource', 'service', ns, name),
  },
})
