// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineResource } from '../types'
import { investigateAction } from '../investigateAction'
import type { k8s } from '../../../wailsjs/go/models'
import { callApp } from '../wails'
import { flatNameCol, flatNsCol, ageCol, controlledByProp, managedByProp } from './common'
import CronJobHistoryTab from '../../components/resource/sections/CronJobHistoryTab.vue'
import { t } from '../../i18n'

export const cronJob = defineResource<k8s.CronJobInfo>({
  id: 'cronjob',
  kind: 'CronJob',
  view: 'cronjobs',
  title: 'CronJobs',
  namespaced: true,
  nav: { group: 'workloads', label: 'Cron Jobs' },

  autoRefresh: true,
  // icon: 'restart',

  columns: [
    flatNameCol,
    { ...flatNsCol, link: (c: any) => c.namespace ? { id: 'namespace', ns: '', name: c.namespace } : null },
    { key: 'schedule', label: 'defs.columns.schedule' },
    {
      key: 'suspend',
      label: 'defs.columns.suspend',
      formatter: c => (c.suspend ? 'true' : 'false'),
    },
    { key: 'active', label: 'defs.columns.active' },
    { key: 'lastSchedule', label: 'defs.columns.lastSchedule' },
    { key: 'nextExecution', label: 'defs.columns.nextExecution' },
    { key: 'timeZone', label: 'defs.columns.timeZone' },
    ageCol,
  ],

  properties: [
    { label: 'defs.columns.schedule', value: c => c.schedule },
    { label: 'defs.columns.suspend', value: c => (c.suspend ? t('defs.values.true') : t('defs.values.false')) },
    { label: 'defs.columns.active', value: c => c.active },
    { label: 'defs.columns.lastSchedule', value: c => c.lastSchedule },
    { label: 'defs.columns.nextExecution', value: c => c.nextExecution },
    { label: 'defs.columns.timeZone', value: c => c.timeZone },
    controlledByProp,
    managedByProp,
  ],

  tabs: [
    { id: 'jobHistory', label: 'defs.tabs.jobHistory', component: CronJobHistoryTab },
  ],

  actions: [
    {
      id: 'trigger',
      label: 'defs.menu.trigger',
      icon: 'play',
      run: (c, ctx) =>
        ctx.perform(
          () => callApp('TriggerCronJob', c.namespace, c.name),
          t('defs.cronJob.triggered', { name: c.name }),
        ),
    },
    {
      id: 'suspend',
      label: 'defs.menu.suspend',
      icon: 'pause',
      when: c => !c.suspend,
      run: (c, ctx) =>
        ctx.perform(
          () => callApp('ToggleCronJobSuspend', c.namespace, c.name),
          t('defs.cronJob.suspended', { name: c.name }),
        ),
    },
    {
      id: 'resume',
      label: 'defs.menu.resume',
      icon: 'play',
      when: c => c.suspend,
      run: (c, ctx) =>
        ctx.perform(
          () => callApp('ToggleCronJobSuspend', c.namespace, c.name),
          t('defs.cronJob.resumed', { name: c.name }),
        ),
    },
    investigateAction('CronJob'),
  ],

  api: {
    list: ns => callApp('GetCronJobs', ns),
    get: (ns, name) => callApp('GetCronJobDetail', ns, name),
    remove: (ns, name) => callApp('DeleteResource', 'cronjob', ns, name),
  },
})
