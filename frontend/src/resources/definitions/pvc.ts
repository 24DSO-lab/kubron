// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { investigateAction } from '../investigateAction'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, flatNsCol, ageCol, controlledByProp, managedByProp } from './common'
import { badgeClass, statusColor } from '../status'
import PVCMetricsSection from '../../components/resource/sections/PVCMetricsSection.vue'
import PVCSelectorSection from '../../components/resource/sections/PVCSelectorSection.vue'

  
import { expandAccessModes } from '../utils'

const MODES: Record<string, string> = {
  RWO: 'ReadWriteOnce',
  ROX: 'ReadOnlyMany',
  RWX: 'ReadWriteMany',
  RWOP: 'ReadWriteOncePod',
}
const accessModes = (p: any): string =>
  String(p.accessModes ?? '').split(',').filter(Boolean).map(m => MODES[m] ?? m).join(', ')

const size = (p: any): string => p.requested || p.capacity || ''
const scLink = (p: any) => (p.storageClass ? { id: 'storageclass', ns: '', name: p.storageClass } : null)

export const persistentVolumeClaim = defineResource<k8s.PVCInfo>({
  id: 'persistentvolumeclaim',
  kind: 'PersistentVolumeClaim',
  actions: [investigateAction('PersistentVolumeClaim')],
  view: 'pvc',
  title: 'PersistentVolumeClaims',
  namespaced: true,
  nav: { group: 'storage', label: 'Persistent Volume Claims' },
  autoRefresh: true,

  // Как в списке-образце: Name, Namespace, Storage class, Size, Pods, Age, Status
  columns: [
    flatNameCol,
    { ...flatNsCol, link: (p: any) => p.namespace ? { id: 'namespace', ns: '', name: p.namespace } : null },
    { key: 'storageClass', label: 'defs.columns.storageClass', link: scLink },
    { key: 'size', label: 'defs.columns.size', formatter: size },
    {
      key: 'pods',
      label: 'defs.columns.pods',
      formatter: (p: any) => (p.pods ?? []).join(', ') || '—',
      link: (p: any) => (p.pods?.length === 1 ? { id: 'pod', ns: p.namespace, name: p.pods[0] } : null),
    },
    ageCol,
    { key: 'status', label: 'common.status', colorFn: p => statusColor(p.status) },
  ],

  headSections: [PVCMetricsSection],

  // Name / Namespace / Age / Created выводит шапка панели; Labels и Annotations — общие секции
  properties: [
    { label: 'defs.props.finalizers', value: (p: any) => (p.finalizers ?? []).join(', ') },
    { label: 'defs.props.accessModes', value: (p: any) => expandAccessModes(p.accessModes) },
    { label: 'defs.props.storageClassName', value: (p: any) => p.storageClass, link: scLink },
    { label: 'defs.props.storage', value: size },
    { label: 'defs.props.volumeMode', value: (p: any) => p.volumeMode },
    {
      label: 'defs.props.volume',
      value: (p: any) => p.volume,
      link: (p: any) => (p.volume ? { id: 'persistentvolume', ns: '', name: p.volume } : null),
    },
    {
      label: 'defs.columns.pods',
      value: () => null,
      links: (p: any) => (p.pods ?? []).map((n: string) => ({ id: 'pod', ns: p.namespace, name: n })),
    },
    { label: 'common.status', value: p => p.status, badge: p => badgeClass(p.status) },
    controlledByProp,
    managedByProp,
  ],
  sections: [PVCSelectorSection],

  api: {
    list: ns => callApp('GetPersistentVolumeClaims', ns),
    remove: (ns, name) => callApp('DeleteResource', 'persistentvolumeclaim', ns, name),
  },
})