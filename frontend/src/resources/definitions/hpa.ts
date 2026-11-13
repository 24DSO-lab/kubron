// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, flatNsCol, ageCol, controlledByProp, managedByProp } from './common'
import ConditionsSection from '../../components/resource/sections/ConditionsSection.vue'
import HpaMetricsSection from '../../components/resource/sections/HpaMetricsSection.vue'

/** Kind цели автоскейлера -> id ресурса в приложении (для ссылки). Прочие kind ссылкой не делаем */
const TARGET_IDS: Record<string, string> = {
  Deployment: 'deployment',
  StatefulSet: 'statefulset',
  ReplicaSet: 'replicaset',
}

const scalingOk = (h: any) => (h.status ?? '').includes('ScalingActive')

export const horizontalPodAutoscaler = defineResource<k8s.HPAInfo>({
  id: 'horizontalpodautoscaler',
  kind: 'HPA',
  view: 'hpa',
  title: 'HorizontalPodAutoscalers',
  namespaced: true,
  nav: { group: 'config', label: 'HPA' },

  autoRefresh: true,

  columns: [
    flatNameCol,
    { ...flatNsCol, link: (h: any) => h.namespace ? { id: 'namespace', ns: '', name: h.namespace } : null },
    { key: 'metricsSummary', label: 'defs.columns.metrics', formatter: (h: any) => h.metricsSummary || '—' },
    { key: 'minReplicas', label: 'defs.columns.minPods' },
    { key: 'maxReplicas', label: 'defs.columns.maxPods' },
    { key: 'currentReplicas', label: 'defs.columns.replicas' },
    { key: 'status', label: 'common.status', formatter: (h: any) => h.status || '—', colorFn: (h: any) => (scalingOk(h) ? 'var(--green)' : 'var(--yellow)') },
    ageCol,
  ],

  properties: [
    {
      label: 'defs.props.reference',
      value: (h: any) => `${h.referenceKind}/${h.referenceName}`,
      link: (h: any) => {
        const id = TARGET_IDS[h.referenceKind]
        return id && h.referenceName ? { id, ns: h.namespace, name: h.referenceName } : null
      },
    },
    { label: 'defs.props.minReplicas', value: (h: any) => h.minReplicas },
    { label: 'defs.props.maxReplicas', value: (h: any) => h.maxReplicas },
    { label: 'defs.props.currentReplicas', value: (h: any) => h.currentReplicas },
    { label: 'defs.props.desiredReplicas', value: (h: any) => h.desiredReplicas },
    { label: 'defs.props.lastScale', value: (h: any) => h.lastScaleTime },
    { label: 'defs.props.finalizers', value: (h: any) => (h.finalizers ?? []).join(', ') },
    controlledByProp,
    managedByProp,
  ],
  sections: [ConditionsSection, HpaMetricsSection],

  api: {
    list: ns => callApp('GetHPA', ns),
    remove: (ns, name) => callApp('DeleteResource', 'horizontalpodautoscaler', ns, name),
  },
})
