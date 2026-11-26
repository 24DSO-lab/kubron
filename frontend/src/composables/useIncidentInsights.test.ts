// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ callApp: vi.fn(), download: vi.fn() }))
vi.mock('../platform/api', () => ({ callApp: api.callApp }))
vi.mock('../platform/files', () => ({ downloadTextFile: api.download }))
vi.mock('./useAuth', () => ({ can: (p: string) => p === 'users.manage' }))

import { decodeBase64Utf8, useIncidentInsights } from './useIncidentInsights'

const ref = { kind: 'Pod', name: 'p' }
const detail = (id: string) => ({
  incident: { id, target: ref, status: 'closed', openedAt: '', updatedAt: '', openedBy: 'a', events: null, evidence: null, eventsUpdatedAt: '', saveLogs: false, version: 1,
    report: { ruleSetVersion: 2, target: ref, observedAt: '', symptoms: null, facts: null, hypotheses: null, recommendations: null, steps: null, missing: null } },
  fragments: null,
})
const b64 = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)))

describe('useIncidentInsights', () => {
  beforeEach(() => { api.callApp.mockReset(); api.download.mockReset() })

  it('статистика: null-срезы Go становятся массивами, право выгрузки — users.manage', async () => {
    api.callApp.mockResolvedValueOnce({ records: 3, unevaluated: 1, computedAt: '', ruleSetVersions: null, hypotheses: null, recommendations: null, symptoms: null, correlations: null, features: null })
    const i = useIncidentInsights(() => 'cl1')
    await i.loadStats()
    expect(api.callApp).toHaveBeenCalledWith('IncidentRuleStats', 'cl1')
    expect(i.stats.value?.hypotheses).toEqual([])
    expect(i.stats.value?.symptoms).toEqual([])
    expect(i.canExport.value).toBe(true)
  })

  it('ошибка статистики не ломает расследование: пометок просто нет', async () => {
    api.callApp.mockRejectedValueOnce(new Error('forbidden'))
    const i = useIncidentInsights(() => 'cl1')
    await expect(i.loadStats()).resolves.toBeUndefined()
    expect(i.stats.value).toBeNull()
    expect(i.statsLoading.value).toBe(false)
  })

  it('предыдущее расследование: найдено — для баннера, недоступно — null', async () => {
    api.callApp.mockResolvedValueOnce(detail('old'))
    const i = useIncidentInsights(() => 'cl1')
    await i.loadPrevious('old')
    expect(api.callApp).toHaveBeenCalledWith('IncidentGet', 'cl1', 'old')
    expect(i.previous.value?.id).toBe('old')
    expect(i.previous.value?.report.facts).toEqual([])

    api.callApp.mockRejectedValueOnce(new Error('not found'))
    await i.loadPrevious('gone')
    expect(i.previous.value).toBeNull()
    await i.loadPrevious('')
    expect(api.callApp).toHaveBeenCalledTimes(2)
  })

  it('устаревший ответ и смена кластера не попадают на экран', async () => {
    let resolveA!: (v: unknown) => void
    api.callApp.mockReturnValueOnce(new Promise((r) => { resolveA = r })).mockResolvedValueOnce(detail('b'))
    const i = useIncidentInsights(() => 'cl1')
    const a = i.loadPrevious('a')
    await i.loadPrevious('b')
    resolveA(detail('a'))
    await a
    expect(i.previous.value?.id).toBe('b')

    let resolveS!: (v: unknown) => void
    api.callApp.mockReturnValueOnce(new Promise((r) => { resolveS = r }))
    const s = i.loadStats()
    i.reset()
    resolveS({ records: 9, unevaluated: 0, computedAt: '', hypotheses: [], recommendations: [], symptoms: [], correlations: [], features: [], ruleSetVersions: [] })
    await s
    expect(i.stats.value).toBeNull()
    expect(i.previous.value).toBeNull()
  })

  it('выгрузка: base64 в UTF-8 текст и скачивание файла; ничего не отправляется само', async () => {
    api.callApp.mockResolvedValueOnce({ filename: 'kubron-rule-stats.json', mime: 'application/json', dataBase64: b64('{"правило":"код"}') })
    const i = useIncidentInsights(() => 'cl1')
    await i.exportStats()
    expect(api.callApp).toHaveBeenCalledWith('IncidentRuleStatsExport', 'cl1')
    expect(api.download).toHaveBeenCalledWith('kubron-rule-stats.json', '{"правило":"код"}', 'application/json')
  })

  it('ошибка выгрузки (нет права) пробрасывается, файл не создаётся', async () => {
    api.callApp.mockRejectedValueOnce(new Error('forbidden'))
    const i = useIncidentInsights(() => 'cl1')
    await expect(i.exportStats()).rejects.toThrow('forbidden')
    expect(api.download).not.toHaveBeenCalled()
  })

  it('decodeBase64Utf8 не портит кириллицу и иероглифы', () => {
    expect(decodeBase64Utf8(b64('Привет 世界'))).toBe('Привет 世界')
  })
})
