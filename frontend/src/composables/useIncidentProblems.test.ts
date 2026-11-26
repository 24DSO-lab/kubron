// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ callApp: vi.fn(), listeners: new Map<string, (...a: unknown[]) => void>() }))

vi.mock('../platform/api', () => ({ callApp: api.callApp }))
vi.mock('../platform/events', () => ({
  onEvent: (name: string, h: (...a: unknown[]) => void) => { api.listeners.set(name, h); return () => api.listeners.delete(name) },
}))
vi.mock('./useAuth', () => ({ can: (p: string) => p === 'operate' }))

import { PROBLEM_CHANGED_EVENT, useIncidentProblems } from './useIncidentProblems'

const node = { kind: 'Node', name: 'n1' }
const summary = (id: string) => ({ id, key: 'k', kind: 'shared_node', source: node, status: 'open', origin: 'auto', memberCount: 1, affectedTotal: 3, updatedAt: '' })
const detail = (id = 'p1') => ({ ...summary(id), members: null, affected: null, affectedTruncated: false, detached: null })
const flush = () => new Promise((r) => setTimeout(r, 0))

describe('useIncidentProblems', () => {
  beforeEach(() => { api.callApp.mockReset(); api.listeners.clear() })

  it('список: фильтр и курсор уходят в RPC, «ещё» дописывает', async () => {
    api.callApp.mockResolvedValueOnce({ items: [summary('a')], nextCursor: 'c1' }).mockResolvedValueOnce({ items: [summary('b')] })
    const p = useIncidentProblems(() => 'cl1')
    await p.loadList()
    expect(api.callApp).toHaveBeenCalledWith('IncidentProblemList', 'cl1', expect.objectContaining({ status: 'open', cursor: '' }))
    await p.loadList(true)
    expect(api.callApp).toHaveBeenLastCalledWith('IncidentProblemList', 'cl1', expect.objectContaining({ cursor: 'c1' }))
    expect(p.items.value.map((i) => i.id)).toEqual(['a', 'b'])
    expect(p.nextCursor.value).toBe('')
  })

  it('null-срезы Go приводятся к массивам; null вместо страницы — пустой список', async () => {
    api.callApp.mockResolvedValueOnce(null).mockResolvedValueOnce(detail())
    const p = useIncidentProblems(() => 'cl1')
    await p.loadList()
    expect(p.items.value).toEqual([])
    await p.select('p1')
    expect(p.detail.value?.members).toEqual([])
    expect(p.detail.value?.affected).toEqual([])
    expect(p.detail.value?.detached).toEqual([])
  })

  it('право operate — подсказка из сессии', () => {
    expect(useIncidentProblems(() => 'cl1').canOperate.value).toBe(true)
  })

  it('действия вызывают нужные RPC и перечитывают список', async () => {
    const p = useIncidentProblems(() => 'cl1')
    api.callApp.mockResolvedValueOnce(detail('p1'))
    await p.select('p1')

    api.callApp.mockResolvedValueOnce(detail('p1')).mockResolvedValueOnce({ items: [summary('p1')] })
    await p.attach('inc-9')
    expect(api.callApp).toHaveBeenCalledWith('IncidentProblemAttach', 'cl1', 'p1', 'inc-9')
    expect(api.callApp).toHaveBeenLastCalledWith('IncidentProblemList', 'cl1', expect.anything())

    api.callApp.mockResolvedValueOnce(detail('p2')).mockResolvedValueOnce({ items: [] })
    await p.merge('p2')
    expect(api.callApp).toHaveBeenCalledWith('IncidentProblemMerge', 'cl1', 'p1', 'p2')
    expect(p.detail.value?.id).toBe('p2')

    api.callApp.mockResolvedValueOnce(detail('p3')).mockResolvedValueOnce({ items: [] })
    await p.create(['a', 'b'])
    expect(api.callApp).toHaveBeenCalledWith('IncidentProblemCreate', 'cl1', ['a', 'b'])
    expect(p.detail.value?.id).toBe('p3')
  })

  it('отсоединение перечитывает проблему; если её не стало — экран закрывается', async () => {
    const p = useIncidentProblems(() => 'cl1')
    api.callApp.mockResolvedValueOnce(detail('p1'))
    await p.select('p1')
    api.callApp.mockResolvedValueOnce({}).mockResolvedValueOnce(detail('p1')).mockResolvedValueOnce({ items: [] })
    await p.detach('inc-1')
    expect(api.callApp).toHaveBeenCalledWith('IncidentProblemDetach', 'cl1', 'inc-1')
    expect(p.detail.value?.id).toBe('p1')

    api.callApp.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error('not found')).mockResolvedValueOnce({ items: [] })
    await p.detach('inc-2')
    expect(p.detail.value).toBeNull()
  })

  it('ошибка действия пробрасывается вызывающему', async () => {
    const p = useIncidentProblems(() => 'cl1')
    api.callApp.mockRejectedValueOnce(new Error('forbidden'))
    await expect(p.create(['a'])).rejects.toThrow('forbidden')
  })

  it('problem:changed перечитывает список, а открытую проблему — только если событие о ней', async () => {
    const p = useIncidentProblems(() => 'cl1')
    api.callApp.mockResolvedValueOnce(detail('p1'))
    await p.select('p1')
    p.listen()
    const h = api.listeners.get(PROBLEM_CHANGED_EVENT)!
    api.callApp.mockResolvedValue({ items: [] })

    h('other')
    await flush()
    expect(api.callApp.mock.calls.filter((c) => c[0] === 'IncidentProblemGet')).toHaveLength(1)

    api.callApp.mockImplementation(async (m: string) => (m === 'IncidentProblemGet' ? detail('p1') : { items: [] }))
    h('p1')
    await flush()
    expect(api.callApp.mock.calls.filter((c) => c[0] === 'IncidentProblemGet')).toHaveLength(2)
  })

  it('reset: ответ прежнего кластера не попадает на экран', async () => {
    let resolve!: (v: unknown) => void
    api.callApp.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    const p = useIncidentProblems(() => 'cl1')
    const pending = p.loadList()
    p.reset()
    resolve({ items: [summary('old')] })
    await pending
    expect(p.items.value).toEqual([])
  })
})
