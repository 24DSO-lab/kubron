// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../wails', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../wails')>()),
  callApp: vi.fn(),
}))

import { callApp } from '../../wails'
import { Perm } from '../../../types/auth'
import type { ResourceDefinition } from '../../types'
import type { FluxAlertInfo, FluxNotifications, FluxProviderInfo, FluxReceiverInfo } from '../../../types/flux'
import { fluxAlert, refList } from './alert'
import { fluxProvider } from './provider'
import { fluxReceiver } from './receiver'

const base = {
  namespace: 'flux-system', age: '1d', interval: '5m', suspended: false, state: 'Ready' as const, sourceRef: null, dependsOn: [],
  conditions: [], revision: '', lastAttemptedRevision: '', lastReconcileAt: '2026-11-07T10:00:00Z', message: '', labels: {}, annotations: {}, created: '',
}
const provider: FluxProviderInfo = { ...base, kind: 'Provider', name: 'slack', type: 'slack', channel: '#ops', address: 'https://hooks.slack.com', secretRef: 'slack-url' }
const alert: FluxAlertInfo = {
  ...base, kind: 'Alert', name: 'on-call', providerRef: 'slack', eventSeverity: 'error',
  eventSources: [{ kind: 'Kustomization', namespace: '', name: '*' }, { kind: 'HelmRelease', namespace: 'apps', name: 'web' }],
}
const receiver: FluxReceiverInfo = { ...base, kind: 'Receiver', name: 'gh', type: 'github', resources: [{ kind: 'GitRepository', namespace: '', name: 'flux-system' }], secretRef: 'gh-token' }
const data: FluxNotifications = { providers: [provider], alerts: [alert], receivers: [receiver] }

const ALL = [
  [fluxAlert, 'flux-alert', 'flux-alerts', 'Alert'],
  [fluxProvider, 'flux-provider', 'flux-providers', 'Provider'],
  [fluxReceiver, 'flux-receiver', 'flux-receivers', 'Receiver'],
] as const

beforeEach(() => { vi.clearAllMocks(); vi.mocked(callApp as any).mockResolvedValue(data) })

describe('регистрация', () => {
  it('id, view, вид и группа меню; уникальны', () => {
    for (const [d, id, view, kind] of ALL) {
      expect(d.id).toBe(id)
      expect(d.view).toBe(view)
      expect(d.kind).toBe(kind)
      expect(d.nav?.group).toBe('flux-notifications')
    }
    expect(new Set(ALL.map(a => a[0].id)).size).toBe(3)
  })

  it('только чтение: нет reconcile, suspend, resume и delete', () => {
    for (const [d] of ALL) {
      const ids = (d.actions ?? []).map(a => a.id)
      for (const forbidden of ['reconcile', 'reconcileWithSource', 'suspend', 'resume', 'delete']) expect(ids).not.toContain(forbidden)
      expect(d.api.remove).toBeUndefined()
      expect(d.bulkDelete).toBe(false)
    }
  })

  it('YAML только у Alert (у Provider и Receiver в нём адрес и путь вебхука)', () => {
    expect(fluxAlert.actions!.map(a => a.id)).toEqual(['editYaml'])
    expect(fluxAlert.actions![0].permission).toBe(Perm.Write)
    expect(fluxProvider.actions ?? []).toEqual([])
    expect(fluxReceiver.actions ?? []).toEqual([])
  })

  it('у каждого вида есть вкладка условий', () => {
    for (const [d] of ALL) expect(d.tabs!.map(t => t.id)).toEqual(['fluxConditions'])
  })
})

describe('данные', () => {
  it('каждый вид берёт свою часть GetFluxNotifications', async () => {
    expect((await fluxProvider.api.list(['a'])).map(x => x.name)).toEqual(['slack'])
    expect((await fluxAlert.api.list(['a'])).map(x => x.name)).toEqual(['on-call'])
    expect((await fluxReceiver.api.list(['a'])).map(x => x.name)).toEqual(['gh'])
    expect(callApp).toHaveBeenCalledWith('GetFluxNotifications', ['a'])
  })
  it('пустые и отсутствующие списки дают пустой массив', async () => {
    vi.mocked(callApp as any).mockResolvedValue({ providers: [], alerts: null, receivers: undefined })
    expect(await fluxProvider.api.list([])).toEqual([])
    expect(await fluxAlert.api.list([])).toEqual([])
    expect(await fluxReceiver.api.list([])).toEqual([])
  })
})

describe('колонки', () => {
  const col = <T>(d: ResourceDefinition<T>, key: string) => d.columns.find(c => c.key === key)!
  it('Provider: тип, канал, схема и хост', () => {
    expect(col(fluxProvider, 'type').formatter!(provider)).toBe('slack')
    expect(col(fluxProvider, 'channel').formatter!(provider)).toBe('#ops')
    expect(col(fluxProvider, 'address').formatter!(provider)).toBe('https://hooks.slack.com')
  })
  it('Alert: провайдер, severity, источники событий', () => {
    expect(col(fluxAlert, 'providerRef').formatter!(alert)).toBe('slack')
    expect(col(fluxAlert, 'eventSeverity').formatter!(alert)).toBe('error')
    expect(col(fluxAlert, 'eventSources').formatter!(alert)).toBe('Kustomization/*, HelmRelease/apps/web')
  })
  it('Receiver: тип и ресурсы', () => {
    expect(col(fluxReceiver, 'type').formatter!(receiver)).toBe('github')
    expect(col(fluxReceiver, 'resources').formatter!(receiver)).toBe('GitRepository/flux-system')
  })
  it('пустые данные не роняют колонки', () => {
    for (const [d] of ALL) for (const c of d.columns) if (c.formatter) expect(() => c.formatter!({} as any)).not.toThrow()
    expect(refList(null)).toBe('')
    expect(refList([])).toBe('')
  })
})

describe('секреты', () => {
  it('контрольные значения не попадают в колонки и свойства (webhookPath в DTO не ждём)', () => {
    const leaky = { ...receiver, webhookPath: '/hook/S3CRET-path' } as unknown as FluxReceiverInfo
    const text = [
      ...fluxReceiver.columns.map(c => (c.formatter ? c.formatter(leaky) : '')),
      ...fluxReceiver.properties!.map(p => String(p.value(leaky) ?? '')),
    ].join('|')
    expect(text).not.toContain('S3CRET')
    expect(text).not.toContain('/hook/')
  })
})
