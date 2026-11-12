// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Report, Summary } from '../types/readiness'

const api = vi.hoisted(() => ({
  readinessInfo: vi.fn(), readinessRun: vi.fn(), readinessHistory: vi.fn(),
  readinessGet: vi.fn(), readinessCompare: vi.fn(), readinessExport: vi.fn(),
}))
vi.mock('../platform/readiness', () => api)

import { resetReadinessForTests, useReadiness } from './useReadiness'

const rep = (id: string): Report => ({
  schemaVersion: 1, id, clusterId: 'c1', createdAt: '2026-11-01T10:00:00Z', current: { gitVersion: 'v1.35.0', minor: '1.35' },
  findings: [], actions: [], coverage: {} as Report['coverage'],
})
const sum = (id: string): Summary => ({ id, clusterId: 'c1', createdAt: '2026-11-01T10:00:00Z', currentVersion: '1.35', blockers: 0, warnings: 0, infos: 0, notChecked: 2 })
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

beforeEach(() => {
  vi.resetAllMocks()
  resetReadinessForTests()
  api.readinessHistory.mockResolvedValue({ items: [sum('r1')] })
  api.readinessCompare.mockResolvedValue({ previousId: 'r0', currentId: 'r1', new: [], resolved: [], changed: [], unchanged: 0 })
})

describe('useReadiness', () => {
  it('loads the window once', async () => {
    api.readinessInfo.mockResolvedValue({ window: ['1.35', '1.36', '1.37'], apiMatrix: {}, catalog: {} })
    const r = useReadiness()
    await r.loadInfo()
    await r.loadInfo()
    expect(api.readinessInfo).toHaveBeenCalledTimes(1)
    expect(r.window.value).toEqual(['1.35', '1.36', '1.37'])
  })

  it('selecting a cluster loads its history and clears the previous report', async () => {
    api.readinessRun.mockResolvedValue(rep('r1'))
    const r = useReadiness()
    await r.selectCluster('c1')
    expect(r.history.value.map(x => x.id)).toEqual(['r1'])
    await r.run()
    expect(r.report.value?.id).toBe('r1')
    await r.selectCluster('c2')
    expect(r.report.value).toBeNull()
    expect(api.readinessHistory).toHaveBeenLastCalledWith('c2', '', 20)
  })

  it('runs with the chosen target and fetches the comparison', async () => {
    api.readinessRun.mockResolvedValue(rep('r1'))
    const r = useReadiness()
    await r.selectCluster('c1')
    r.target.value = '1.36'
    await r.run()
    expect(api.readinessRun).toHaveBeenCalledWith('c1', '1.36')
    expect(api.readinessCompare).toHaveBeenCalledWith('c1', 'r1')
    expect(r.diff.value?.previousId).toBe('r0')
    expect(r.diffUnavailable.value).toBe(false)
  })

  it('a first report has nothing to compare with, which is not an error', async () => {
    api.readinessRun.mockResolvedValue(rep('r1'))
    api.readinessCompare.mockRejectedValue(new Error('readiness report not found'))
    const r = useReadiness()
    await r.selectCluster('c1')
    await r.run()
    expect(r.diff.value).toBeNull()
    expect(r.diffUnavailable.value).toBe(true)
    expect(r.error.value).toBe('')
  })

  it('shows the error of a failed run and keeps no report', async () => {
    api.readinessRun.mockRejectedValue(new Error('boom'))
    const r = useReadiness()
    await r.selectCluster('c1')
    await r.run()
    expect(r.report.value).toBeNull()
    expect(r.error.value).not.toBe('')
    expect(r.running.value).toBe(false)
  })

  it('drops a response that arrives after the cluster was changed', async () => {
    const d = deferred<Report>()
    api.readinessRun.mockReturnValue(d.promise)
    const r = useReadiness()
    await r.selectCluster('c1')
    const p = r.run()
    await r.selectCluster('c2')
    d.resolve(rep('late'))
    await p
    expect(r.report.value).toBeNull()
  })

  it('does not start a second run while one is in progress', async () => {
    const d = deferred<Report>()
    api.readinessRun.mockReturnValue(d.promise)
    const r = useReadiness()
    await r.selectCluster('c1')
    const p = r.run()
    await r.run()
    expect(api.readinessRun).toHaveBeenCalledTimes(1)
    d.resolve(rep('r1'))
    await p
  })

  it('pages through the history with the cursor', async () => {
    api.readinessHistory.mockResolvedValueOnce({ items: [sum('r3')], next: 'cur' }).mockResolvedValueOnce({ items: [sum('r2')] })
    const r = useReadiness()
    await r.selectCluster('c1')
    expect(r.hasMoreHistory.value).toBe(true)
    await r.loadHistory()
    expect(api.readinessHistory).toHaveBeenLastCalledWith('c1', 'cur', 20)
    expect(r.history.value.map(x => x.id)).toEqual(['r3', 'r2'])
    expect(r.hasMoreHistory.value).toBe(false)
  })

  it('opens a stored report', async () => {
    api.readinessGet.mockResolvedValue(rep('r7'))
    const r = useReadiness()
    await r.selectCluster('c1')
    await r.open('r7')
    expect(r.report.value?.id).toBe('r7')
    expect(api.readinessCompare).toHaveBeenCalledWith('c1', 'r7')
  })

  it('exports the current report', async () => {
    api.readinessRun.mockResolvedValue(rep('r1'))
    api.readinessExport.mockResolvedValue({ format: 'markdown', filename: 'x.md', mime: 'text/markdown', content: '# x' })
    const r = useReadiness()
    await r.selectCluster('c1')
    expect(await r.exportReport('markdown')).toBeNull() // отчёта ещё нет
    await r.run()
    const out = await r.exportReport('markdown')
    expect(api.readinessExport).toHaveBeenCalledWith('c1', 'r1', 'markdown')
    expect(out?.content).toBe('# x')
  })
})
