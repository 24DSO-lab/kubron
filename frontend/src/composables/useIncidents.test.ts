// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ callApp: vi.fn(), listeners: new Map<string, (...a: unknown[]) => void>(), download: vi.fn() }))

vi.mock('../platform/api', () => ({ callApp: api.callApp }))
vi.mock('../platform/events', () => ({
  onEvent: (name: string, h: (...a: unknown[]) => void) => { api.listeners.set(name, h); return () => api.listeners.delete(name) },
}))
vi.mock('../platform/files', () => ({ downloadTextFile: api.download }))
vi.mock('./useAuth', () => ({ can: () => true }))

import { INCIDENT_UPDATED_EVENT, useIncidents } from './useIncidents'

const ref = { kind: 'Deployment', namespace: 'shop', name: 'web' }
const detail = (id = 'i1', status = 'open') => ({
  incident: { id, target: ref, status, openedAt: '', updatedAt: '', openedBy: 'a', events: null, evidence: null, eventsUpdatedAt: '', saveLogs: true, version: 1,
    report: { ruleSetVersion: 1, target: ref, observedAt: '', symptoms: null, facts: null, hypotheses: null, recommendations: null, steps: null, missing: null } },
  fragments: null,
})

describe('useIncidents', () => {
  beforeEach(() => { api.callApp.mockReset(); api.listeners.clear(); api.download.mockReset() })

  it('открывает расследование и приводит null-срезы Go к массивам', async () => {
    api.callApp.mockResolvedValueOnce(detail())
    const inc = useIncidents(() => 'cl1')
    await inc.open({ kind: 'Deployment', namespace: 'shop', name: 'web', disableLogs: false })
    expect(api.callApp).toHaveBeenCalledWith('IncidentOpen', 'cl1', expect.objectContaining({ name: 'web' }))
    expect(inc.detail.value?.incident.report.facts).toEqual([])
    expect(inc.detail.value?.fragments).toEqual([])
  })

  it('список постраничный: «ещё» дописывает и передаёт курсор', async () => {
    api.callApp.mockResolvedValueOnce({ items: [{ id: 'a' }], nextCursor: 'c1' }).mockResolvedValueOnce({ items: [{ id: 'b' }] })
    const inc = useIncidents(() => 'cl1')
    await inc.loadList()
    expect(inc.nextCursor.value).toBe('c1')
    await inc.loadList(true)
    expect(api.callApp).toHaveBeenLastCalledWith('IncidentList', 'cl1', expect.objectContaining({ cursor: 'c1' }))
    expect(inc.items.value.map((i) => i.id)).toEqual(['a', 'b'])
    expect(inc.nextCursor.value).toBe('')
  })

  it('ошибка бэкенда пробрасывается, состояние загрузки сбрасывается', async () => {
    api.callApp.mockRejectedValueOnce(new Error('boom'))
    const inc = useIncidents(() => 'cl1')
    await expect(inc.loadList()).rejects.toThrow('boom')
    expect(inc.loading.value).toBe(false)
  })

  it('результат, фрагменты и экспорт идут через методы бэкенда выбранного расследования', async () => {
    api.callApp.mockResolvedValueOnce(detail()).mockResolvedValue(detail('i1', 'closed'))
    const inc = useIncidents(() => 'cl1')
    await inc.open({ kind: 'Deployment', namespace: 'shop', name: 'web', disableLogs: false })
    await inc.markOutcome('resolved', 'h1', 'ok')
    expect(api.callApp).toHaveBeenLastCalledWith('IncidentMarkOutcome', 'cl1', 'i1', { result: 'resolved', confirmedHypothesisId: 'h1', note: 'ok' })
    await inc.saveFragment('web-1', 'app', true)
    expect(api.callApp).toHaveBeenCalledWith('IncidentSaveFragment', 'cl1', 'i1', expect.objectContaining({ pod: 'web-1', previous: true }))
    await inc.readFragment('fr1')
    expect(api.callApp).toHaveBeenLastCalledWith('IncidentReadFragment', 'cl1', 'i1', 'fr1')
    api.callApp.mockResolvedValueOnce('{"json":true}')
    await inc.exportIncident(false)
    expect(api.callApp).toHaveBeenLastCalledWith('IncidentExport', 'cl1', 'i1', false)
    expect(api.download).toHaveBeenCalledWith('kubron-incident-i1.json', '{"json":true}', 'application/json')
  })

  it('события обновления перечитывают только открытое выбранное расследование', async () => {
    api.callApp.mockResolvedValue(detail())
    const inc = useIncidents(() => 'cl1')
    const stop = inc.listen()
    await inc.select('i1')
    api.callApp.mockClear()
    api.listeners.get(INCIDENT_UPDATED_EVENT)?.('other')
    expect(api.callApp).not.toHaveBeenCalled()
    api.listeners.get(INCIDENT_UPDATED_EVENT)?.('i1')
    expect(api.callApp).toHaveBeenCalledWith('IncidentGet', 'cl1', 'i1')
    stop()
    expect(api.listeners.has(INCIDENT_UPDATED_EVENT)).toBe(false)
  })

  it('без выбранного расследования действия ничего не вызывают', async () => {
    const inc = useIncidents(() => 'cl1')
    await inc.refreshEvents()
    await inc.markOutcome('resolved', '', '')
    await inc.remove()
    expect(api.callApp).not.toHaveBeenCalled()
    await expect(inc.readFragment('x')).rejects.toThrow()
  })
})
