// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { badgeClass, statusColor } from './status'

describe('badgeClass', () => {
  it.each([
    ['Running', 'rd-ok'],
    ['Healthy', 'rd-ok'],
    ['Synced', 'rd-ok'],
    ['Bound', 'rd-ok'],
    ['Completed', 'rd-ok'],
    ['Pending', 'rd-warn'],
    ['ContainerCreating', 'rd-warn'],
    ['Terminating', 'rd-warn'],
    ['OutOfSync', 'rd-warn'],
    ['CrashLoopBackOff', 'rd-err'],
    ['ImagePullBackOff', 'rd-err'],
    ['Failed', 'rd-err'],
    ['Degraded', 'rd-err'],
    ['Missing', 'rd-err'],
  ])('%s -> %s', (status, cls) => {
    expect(badgeClass(status)).toBe(cls)
  })

  it('регистр не важен', () => {
    expect(badgeClass('RUNNING')).toBe('rd-ok')
    expect(badgeClass('crashloopbackoff')).toBe('rd-err')
  })

  it('N/N Running — зелёный, частичный — жёлтый', () => {
    expect(badgeClass('3/3 Running')).toBe('rd-ok')
    expect(badgeClass('1/1 Ready')).toBe('rd-ok')
    expect(badgeClass('1/3 Running')).toBe('rd-warn')
    expect(badgeClass('0/2 Ready')).toBe('rd-warn')
  })

  it('неизвестное и пустое — rd-unk', () => {
    expect(badgeClass('SomethingNew')).toBe('rd-unk')
    expect(badgeClass('')).toBe('rd-unk')
    expect(badgeClass(undefined)).toBe('rd-unk')
  })
})

describe('statusColor', () => {
  it('соответствует классу бейджа', () => {
    expect(statusColor('Running')).toBe('var(--green)')
    expect(statusColor('Pending')).toBe('var(--yellow)')
    expect(statusColor('Failed')).toBe('var(--red)')
    expect(statusColor('whatever')).toBe('var(--text-gray)')
    expect(statusColor(undefined)).toBe('var(--text-gray)')
  })
})
