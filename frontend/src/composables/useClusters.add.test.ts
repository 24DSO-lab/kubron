// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../platform/api', () => ({ callApp: vi.fn() }))

import { callApp } from '../platform/api'
import { useClusters } from './useClusters'
import type { ClusterSettings } from '../types'

const settings = { displayName: 'prod' } as ClusterSettings

describe('добавление кластера в сервисном режиме', () => {
  beforeEach(() => {
    vi.mocked(callApp).mockReset()
    vi.mocked(callApp).mockImplementation(async (...args: unknown[]) => {
      const method = args[0] as string
      return method === 'GetClusterStatuses' ? [] : { id: 'prod' }
    })
  })

  it('без команды — прежний метод десктопа', async () => {
    await useClusters().addClusterFromText('kc', settings)
    expect(callApp).toHaveBeenCalledWith('AddClusterFromText', 'kc', settings)
  })

  it('с командой — AddClusterToTeam', async () => {
    await useClusters().addClusterFromText('kc', settings, 't1')
    expect(callApp).toHaveBeenCalledWith('AddClusterToTeam', 't1', 'kc', settings)
  })

  it('общая учётная запись команды — флаг в настройках, пароль не передаётся', async () => {
    const shared = { ...settings, sharedAccount: true } as ClusterSettings
    const res = await useClusters().addClusterFromText('kc', shared, 't1')
    expect(res.ok).toBe(true)
    expect(callApp).toHaveBeenCalledWith('AddClusterToTeam', 't1', 'kc', shared)
  })

  it('in-cluster — AddInClusterToTeam', async () => {
    await useClusters().addInCluster(settings, 't1')
    expect(callApp).toHaveBeenCalledWith('AddInClusterToTeam', 't1', settings)
  })

  it('ошибка бэкенда возвращается как итог, а не исключение', async () => {
    vi.mocked(callApp).mockRejectedValueOnce(new Error('boom'))
    const res = await useClusters().addInCluster(settings, 't1')
    expect(res.ok).toBe(false)
  })
})
