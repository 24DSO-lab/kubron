// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNsCol, ageCol } from './common'
import { badgeClass, statusColor } from '../status'
import { applicationReplicas, urlsText } from '../applications'

import ApplicationMetricsSection from '../../components/resource/sections/ApplicationMetricsSection.vue'
import ApplicationPodsSection from '../../components/resource/sections/ApplicationPodsSection.vue'
import ApplicationResourcesSection from '../../components/resource/sections/ApplicationResourcesSection.vue'

/** «ready/desired» по подам всех workload-ов приложения */
const podsText = (a: any) => {
  const { ready, desired } = applicationReplicas(a)
  return `${ready}/${desired}`
}

/**
 * Applications (как в Lens): workload-ы, сгруппированные по recommended labels
 * app.kubernetes.io/instance + app.kubernetes.io/name. Это вид поверх Deployment/StatefulSet/DaemonSet,
 * а не отдельный k8s-ресурс: без YAML и удаления.
 */
export const application = defineResource<k8s.ApplicationInfo>({
  // id не 'application': он занят Argo CD Application (definitions/argocd/application.ts),
  // а реестр резолвит определения по id (registry.ts) — дубликат молча перекрыл бы Argo-вкладки
  id: 'app-instance',
  kind: 'ApplicationInstance',
  view: 'applications',
  title: 'Applications',
  namespaced: true,
  nav: { label: 'Applications' },
  icon: 'dependencies',

  autoRefresh: true,
  editable: false,

  columns: [
    { key: 'instance', label: 'defs.columns.instance', className: 'resource-name' },
    { key: 'application', label: 'defs.columns.application' },
    flatNsCol,
    { key: 'version', label: 'defs.columns.version' },
    { key: 'managedBy', label: 'defs.columns.managedBy' },
    {
      key: 'pods',
      label: 'defs.columns.pods',
      formatter: podsText,
      sortValue: (a: any) => applicationReplicas(a).ready,
      colorFn: (a: any) => {
        const { ready, desired } = applicationReplicas(a)
        return ready >= desired ? 'var(--green)' : 'var(--yellow)'
      },
    },
    ageCol,
    { key: 'status', label: 'common.status', colorFn: (a: any) => statusColor(a.status) },
  ],

  headSections: [ApplicationMetricsSection],
  properties: [
    { label: 'common.status', value: a => a.status, badge: a => badgeClass(a.status) },
    { label: 'defs.columns.application', value: a => a.application },
    { label: 'defs.columns.version', value: a => a.version },
    { label: 'defs.columns.managedBy', value: a => a.managedBy },
    { label: 'defs.props.component', value: a => a.component, hideEmpty: true },
    { label: 'defs.props.partOf', value: a => a.partOf, hideEmpty: true },
    { label: 'defs.props.internalUrls', multiline: true, value: a => urlsText(a.internalUrls) },
    { label: 'defs.props.exposedUrls', multiline: true, value: a => urlsText(a.exposedUrls) },
    { label: 'common.name', value: a => a.name },
    {
      label: 'common.namespace',
      value: a => a.namespace,
      link: a => (a.namespace ? { id: 'namespace', ns: '', name: a.namespace } : null),
    },
  ],
  sections: [ApplicationPodsSection, ApplicationResourcesSection],

  api: {
    list: ns => callApp('GetApplications', ns),
  },
})
