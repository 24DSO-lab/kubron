// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../resources/wails', () => ({ callApp: vi.fn() }))

import { callApp } from '../resources/wails'
import { emptySpec, parseList, previewEmpty, priorityRank, refLabel, sortItems, specReady, statusTone, useBaseline } from './useBaseline'
import type { BaselineItem } from '../types/baseline'

const call = vi.mocked(callApp) as unknown as ReturnType<typeof vi.fn>

const baseline = (id: string, active = 'v1') => ({
  id, name: id, namespaces: ['prod'], selector: '', kinds: [], refs: [], activeVersionId: active, createdBy: 'a', createdAt: '2026-11-12T10:00:00Z',
})

beforeEach(() => call.mockReset())

describe('чистые функции', () => {
  it('parseList: запятые, пробелы, повторы', () => {
    expect(parseList('prod, stage  prod\nqa,')).toEqual(['prod', 'stage', 'qa'])
    expect(parseList('  ')).toEqual([])
  })

  it('specReady: нужны пространства имён или ресурсы', () => {
    expect(specReady(emptySpec())).toBe(false)
    expect(specReady({ ...emptySpec(), namespaces: ['a'] })).toBe(true)
    expect(specReady({ ...emptySpec(), refs: [{ version: 'v1', kind: 'ConfigMap', name: 'x' }] })).toBe(true)
  })

  it('«не проверено» не выглядит как «соответствует»', () => {
    expect(statusTone('match')).toBe('ok')
    expect(statusTone('unchecked')).not.toBe('ok')
    expect(statusTone('missing')).toBe('bad')
  })

  it('sortItems: изменённые и важные выше, соответствующие внизу', () => {
    const mk = (name: string, status: BaselineItem['status'], priority?: BaselineItem['priority']): BaselineItem =>
      ({ ref: { version: 'v1', kind: 'Deployment', namespace: 'p', name }, status, priority })
    const out = sortItems([mk('a', 'match'), mk('b', 'unchecked'), mk('c', 'changed', 'low'), mk('d', 'changed', 'high'), mk('e', 'missing')])
    expect(out.map(i => i.ref.name)).toEqual(['d', 'c', 'e', 'b', 'a'])
  })

  it('refLabel и priorityRank', () => {
    expect(refLabel({ version: 'v1', kind: 'Service', namespace: 'p', name: 's' })).toBe('Service p/s')
    expect(refLabel({ version: 'v1', kind: 'Node', name: 'n1' })).toBe('Node n1')
    expect(priorityRank('high')).toBeGreaterThan(priorityRank('low'))
    expect(priorityRank(undefined)).toBe(0)
  })

  it('previewEmpty: пустой предпросмотр не даёт создать версию', () => {
    expect(previewEmpty(null)).toBe(true)
    expect(previewEmpty({ token: 't', expiresAt: '', objects: 0, hashed: 0, hasPrevious: false, rulesChanged: false, unchanged: 0 })).toBe(true)
    expect(previewEmpty({ token: 't', expiresAt: '', objects: 2, hashed: 0, hasPrevious: false, rulesChanged: false, unchanged: 0 })).toBe(false)
  })
})

describe('useBaseline', () => {
  it('loadList выбирает первый baseline и сбрасывает пропавший', async () => {
    call.mockResolvedValueOnce([baseline('b1'), baseline('b2')])
    const s = useBaseline()
    await s.loadList()
    expect(s.selectedId.value).toBe('b1')
    s.selectedId.value = 'gone'
    call.mockResolvedValueOnce([baseline('b2')])
    await s.loadList()
    expect(s.selectedId.value).toBe('b2')
  })

  it('ошибка бэкенда показывается, состояние не ломается', async () => {
    call.mockRejectedValueOnce('boom')
    const s = useBaseline()
    await s.loadList()
    expect(s.error.value).toBeTruthy()
    expect(s.baselines.value).toEqual([])
  })

  it('create передаёт подтверждение и токен; без ответа — false', async () => {
    const s = useBaseline()
    call.mockRejectedValueOnce('confirmation is required')
    expect(await s.create('n', emptySpec(), 'c', 'tok', false)).toBe(false)
    expect(call).toHaveBeenCalledWith('BaselineCreate', { name: 'n', spec: emptySpec(), comment: 'c', token: 'tok', confirmed: false })
  })

  it('check: результат остаётся после обновления истории', async () => {
    const s = useBaseline()
    s.selectedId.value = 'b1'
    const res = { id: 'r1', summary: { total: 1 }, items: [] }
    call.mockImplementation(async (fn: string) => {
      if (fn === 'DriftCheck') return res
      if (fn === 'BaselineVersions' || fn === 'DriftResults') return { items: [] }
      return []
    })
    expect(await s.check()).toBe(true)
    expect(s.result.value).toEqual(res)
    expect(s.checking.value).toBe(false)
  })

  it('removeException убирает исключение из списка после успеха', async () => {
    const s = useBaseline()
    s.selectedId.value = 'b1'
    s.exceptions.value = [{ id: 'e1' } as any, { id: 'e2' } as any]
    call.mockResolvedValueOnce(undefined)
    expect(await s.removeException('e1')).toBe(true)
    expect(s.exceptions.value.map(e => e.id)).toEqual(['e2'])
  })

  it('смена выбора во время загрузки не затирает новый baseline', async () => {
    const s = useBaseline()
    let release!: (v: unknown) => void
    call.mockImplementation((fn: string) =>
      fn === 'BaselineVersions' ? new Promise(r => { release = r }) : Promise.resolve(fn === 'DriftResults' ? { items: [] } : []))
    const p = s.loadDetails('b1')
    s.selectedId.value = 'b2'
    release({ items: [{ id: 'old' }] })
    await p
    expect(s.versions.value).toEqual([])
  })
})
