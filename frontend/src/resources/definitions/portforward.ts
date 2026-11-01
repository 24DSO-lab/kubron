// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { k8s as models } from '../../../wailsjs/go/models'
import { defineResource } from '../types'
import { Perm } from '../../types/auth'
import { callApp } from '../wails'
import { useK8s } from '../../composables/useK8s'
import { flatNsCol } from './common'
import { badgeClass, statusColor } from '../status'
import { openUrl } from '../../platform/files'
import { t } from '../../i18n'
import { getPortForwardUrl } from '../../composables/portForwardUrl'

const k8s = useK8s()

export const portForward = defineResource<models.PortForwardSession>({
  id: 'portforward',
  kind: 'Port Forward',
  view: 'portforwarding',
  title: 'Port Forwarding',
  namespaced: false,
  nav: { group: 'network', label: 'Port Forwarding' },
  autoRefresh: true,
  // локальные сессии приложения, не ресурс k8s: без YAML и удаления
  editable: false,

  columns: [
    { key: 'target', label: 'defs.columns.target', className: 'resource-name' },
    { ...flatNsCol, link: (pf: any) => pf.namespace ? { id: 'namespace', ns: '', name: pf.namespace } : null },
    { key: 'localPort', label: 'defs.columns.localPort', formatter: (pf: any) => (pf.mode === 'proxy' ? t('sections.portForward.viaProxy') : `localhost:${pf.localPort}`) },
    { key: 'podPort', label: 'defs.columns.remotePort' },
    { key: 'status', label: 'common.status', colorFn: pf => statusColor(pf.status) },
  ],

  properties: [
    { label: 'defs.columns.target', value: (pf: any) => pf.target },
    { label: 'common.namespace', value: pf => pf.namespace, link: (pf: any) => pf.namespace ? { id: 'namespace', ns: '', name: pf.namespace } : null },
    { label: 'defs.props.pod', value: pf => pf.pod, link: (pf: any) => ({ id: 'pod', ns: pf.namespace, name: pf.pod }) },
    {
      label: 'defs.props.service',
      value: (pf: any) => (pf.service ? `${pf.service}:${pf.servicePort}` : '—'),
      link: (pf: any) => (pf.service ? { id: 'service', ns: pf.namespace, name: pf.service } : null),
    },
    { label: 'defs.columns.localPort', value: (pf: any) => (pf.mode === 'proxy' ? t('sections.portForward.viaProxy') : pf.localPort) },
    { label: 'defs.columns.remotePort', value: pf => pf.podPort },
    { label: 'common.status', value: pf => pf.status, badge: pf => badgeClass(pf.status) },
    { label: 'common.error', value: (pf: any) => pf.error || '—' },
  ],

  actions: [
    {
      id: 'open',
      permission: Perm.PortForward,
      label: 'defs.menu.openInBrowser',
      icon: 'open-browser',
      when: (pf: any) => pf.status === 'active',
      run: async (pf: any) => openUrl(await getPortForwardUrl(pf.id)),
    },
    {
      id: 'stop',
      permission: Perm.PortForward,
      label: 'defs.menu.stop',
      icon: 'stop',
      danger: true,
      run: (pf: any, ctx) =>
        ctx.perform(
          () => callApp('StopPortForward', pf.id),
          t('defs.portForward.stopped', { port: pf.localPort, target: pf.target }),
        ),
    },
  ],

  // Сессии запускают и из панелей pod/service: список обновляется сам
  refreshOn: () => k8s.portForwards.value,

  api: {
    list: async () => {
      await k8s.refreshPortForwards()
      return [...k8s.portForwards.value]
    },
    // «Удалить» сессию = остановить туннель и убрать запись; name сессии равен её id
    remove: (_ns, name) => callApp('StopPortForward', name),
  },
})