// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

// Resource definitions transitively import Monaco Editor. Keep this logic-only test in Node.
vi.mock('monaco-editor', () => ({ editor: {} }))
vi.mock('monaco-editor/editor/editor.api', () => ({ editor: {}, KeyMod: {}, KeyCode: {} }))
vi.mock('monaco-editor/editor/editor.worker?worker', () => ({ default: class {} }))

const { switchView } = vi.hoisted(() => ({ switchView: vi.fn() }))
vi.mock('../composables/useK8s', () => ({ useK8s: () => ({ switchView }) }))

import { pendingInvestigation, takeInvestigation } from '../composables/incidentLaunch'
import { Perm } from '../types/auth'
import { investigateAction } from './investigateAction'
import { cronJob } from './definitions/cronjob'
import { deployment } from './definitions/deployment'
import { horizontalPodAutoscaler } from './definitions/hpa'
import { ingress } from './definitions/ingress'
import { job } from './definitions/job'
import { node } from './definitions/node'
import { pod } from './definitions/pod'
import { persistentVolumeClaim } from './definitions/pvc'
import { replicaSet } from './definitions/replicaset'
import { service } from './definitions/service'
import { statefulSet } from './definitions/statefulset'
import type { ActionContext } from './types'

const closeDetail = vi.fn()
const ctx = { closeDetail } as unknown as ActionContext

beforeEach(() => {
  switchView.mockClear()
  closeDetail.mockClear()
  pendingInvestigation.value = null
})

describe('кнопка «Расследовать»', () => {
  const cases: [string, { actions?: { id: string }[] }, string][] = [
    ['Service', service, 'Service'],
    ['PVC', persistentVolumeClaim, 'PersistentVolumeClaim'],
    ['Node', node, 'Node'],
    ['Ingress', ingress, 'Ingress'],
    ['HPA', horizontalPodAutoscaler, 'HorizontalPodAutoscaler'],
    ['StatefulSet', statefulSet, 'StatefulSet'],
    ['Job', job, 'Job'],
    ['CronJob', cronJob, 'CronJob'],
    ['Pod', pod, 'Pod'],
    ['Deployment', deployment, 'Deployment'],
    ['ReplicaSet', replicaSet, 'ReplicaSet'],
  ]

  it.each(cases)('%s: действие есть в карточке', (_n, def) => {
    expect(def.actions?.filter((a) => a.id === 'investigate')).toHaveLength(1)
  })

  it('доступна с правом чтения (открытие расследования не требует записи)', () => {
    expect(investigateAction('Service').permission).toBe(Perm.Read)
  })

  it('кладёт цель, закрывает панель и переключает экран на расследования', () => {
    investigateAction('Service').run({ name: 'web', namespace: 'shop' }, ctx)
    expect(closeDetail).toHaveBeenCalledOnce()
    expect(switchView).toHaveBeenCalledWith('incidents')
    expect(takeInvestigation()).toEqual({ kind: 'Service', namespace: 'shop', name: 'web' })
    expect(takeInvestigation()).toBeNull() // цель забирается один раз
  })

  it('Node: namespace пустой, даже если у объекта он случайно задан', () => {
    investigateAction('Node').run({ name: 'worker-1', namespace: 'x' }, ctx)
    expect(takeInvestigation()).toEqual({ kind: 'Node', namespace: '', name: 'worker-1' })
  })

  it('HPA: в расследование уходит вид из diagnose, а не def.kind', () => {
    investigateAction('HorizontalPodAutoscaler').run({ name: 'web', namespace: 'shop' }, ctx)
    expect(takeInvestigation()?.kind).toBe('HorizontalPodAutoscaler')
  })
})
