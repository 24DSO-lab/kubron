// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

// Язык не влияет на тесты: t() возвращает ключ и параметры
vi.mock('../i18n', () => ({ t: (k: string, p?: Record<string, unknown>) => (p ? `${k}${JSON.stringify(p)}` : k) }))
vi.mock('./wails', () => ({ callApp: vi.fn() }))
vi.mock('./confirm', () => ({ askConfirm: vi.fn(async () => true) }))
vi.mock('./store', () => ({ reloadActive: vi.fn(async () => {}) }))
vi.mock('./registry', () => ({ getResourceById: vi.fn() }))
vi.mock('./bulk', () => ({ runBatch: vi.fn() }))
vi.mock('./templates', () => ({ manifestTemplate: vi.fn() }))
vi.mock('../composables/errorText', () => ({ errorText: (e: unknown) => String((e as Error)?.message ?? e) }))
const { notifySuccess, notifyError, openTerminalTab, can } = vi.hoisted(() => ({
  notifySuccess: vi.fn(),
  notifyError: vi.fn(),
  openTerminalTab: vi.fn(async () => {}),
  can: vi.fn(),
}))
vi.mock('../composables/useNotifications', () => ({ useNotifications: () => ({ notifySuccess, notifyError }) }))
vi.mock('../composables/useTabbedPanel', () => ({ globalTabbedPanel: { openTerminalTab } }))
vi.mock('../composables/useAuth', () => ({ can }))

import { callApp } from './wails'
import { reloadActive } from './store'
import { Perm } from '../types/auth'
import { debugTarget, resourceActions } from './actions'

const call = vi.mocked(callApp) as unknown as ReturnType<typeof vi.fn>
const grant = (...perms: string[]) => can.mockImplementation((p: string) => perms.includes(p))

beforeEach(() => {
  vi.clearAllMocks()
  debugTarget.value = null
  grant(Perm.Debug, Perm.Exec)
})

describe('requestDebug', () => {
  it('открывает диалог с подом и контейнерами', () => {
    resourceActions.requestDebug('prod', 'web-1', ['app', 'sidecar'], 'app')
    expect(debugTarget.value).toEqual({ namespace: 'prod', pod: 'web-1', containers: ['app', 'sidecar'], target: 'app' })
    expect(notifyError).not.toHaveBeenCalled()
  })

  it('без права debug диалог не открывается', () => {
    grant(Perm.Exec)
    resourceActions.requestDebug('prod', 'web-1', ['app'])
    expect(debugTarget.value).toBeNull()
    expect(notifyError).toHaveBeenCalledTimes(1)
  })

  it('без права exec диалог не открывается: подключиться к контейнеру всё равно было бы нельзя', () => {
    grant(Perm.Debug)
    resourceActions.requestDebug('prod', 'web-1', ['app'])
    expect(debugTarget.value).toBeNull()
    expect(notifyError).toHaveBeenCalledTimes(1)
  })
})

describe('confirmDebug', () => {
  const params = { image: 'busybox:1', profile: 'restricted', target: 'app' }

  it('создаёт контейнер, закрывает диалог и открывает к нему терминал в режиме attach', async () => {
    resourceActions.requestDebug('prod', 'web-1', ['app'], 'app')
    call.mockResolvedValueOnce({ namespace: 'prod', pod: 'web-1', container: 'kubron-debug-abcde' })

    await resourceActions.confirmDebug(params)

    expect(call).toHaveBeenCalledWith('StartDebugContainer', 'prod', 'web-1', 'app', 'busybox:1', 'restricted')
    expect(debugTarget.value).toBeNull()
    expect(openTerminalTab).toHaveBeenCalledWith('pod', 'web-1', 'prod', 'web-1', 'kubron-debug-abcde', 'attach')
    expect(notifySuccess).toHaveBeenCalledTimes(1)
    expect(reloadActive).toHaveBeenCalled()
  })

  it('ошибка бэкенда уходит диалогу: он остаётся открытым, терминал не открывается', async () => {
    resourceActions.requestDebug('prod', 'web-1', ['app'])
    call.mockRejectedValueOnce(new Error('image "x" is not allowed by the administrator'))

    await expect(resourceActions.confirmDebug(params)).rejects.toThrow('not allowed')

    expect(debugTarget.value).not.toBeNull()
    expect(openTerminalTab).not.toHaveBeenCalled()
    expect(notifySuccess).not.toHaveBeenCalled()
  })

  it('без открытого диалога ничего не делает', async () => {
    await resourceActions.confirmDebug(params)
    expect(call).not.toHaveBeenCalled()
    expect(openTerminalTab).not.toHaveBeenCalled()
  })
})

describe('cancelDebug', () => {
  it('закрывает диалог без обращения к бэкенду', () => {
    resourceActions.requestDebug('prod', 'web-1', ['app'])
    resourceActions.cancelDebug()
    expect(debugTarget.value).toBeNull()
    expect(call).not.toHaveBeenCalled()
  })
})
