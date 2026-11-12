// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { diffHasChanges, findingTitle, groupFindings, isNotChecked, messageText, notCheckedFindings, partialSources, safeUrl, uncheckedSources } from './readinessView'
import type { Coverage, Diff, Finding } from '../../types/readiness'

const f = (over: Partial<Finding>): Finding => ({
  key: 'k', type: 'api', status: 'deprecated', severity: 'warning', priority: 1,
  explanation: { code: 'x' }, source: 'live_api', observedAt: '2026-11-01T10:00:00Z', ...over,
})

describe('groupFindings', () => {
  it('groups by severity and orders by priority', () => {
    const g = groupFindings([
      f({ key: 'a', severity: 'info', priority: 5 }),
      f({ key: 'b', severity: 'blocker', priority: 2 }),
      f({ key: 'c', severity: 'blocker', priority: 1 }),
      f({ key: 'd', severity: 'warning', priority: 3 }),
    ])
    expect(g.blocker.map(x => x.key)).toEqual(['c', 'b'])
    expect(g.warning.map(x => x.key)).toEqual(['d'])
    expect(g.info.map(x => x.key)).toEqual(['a'])
  })
  it('returns empty groups for no findings', () => {
    expect(groupFindings([])).toEqual({ blocker: [], warning: [], info: [] })
  })
})

describe('not checked', () => {
  it('treats not_checked and partial as not checked, nothing else', () => {
    expect(isNotChecked('not_checked')).toBe(true)
    expect(isNotChecked('partial')).toBe(true)
    for (const s of ['ok', 'confirmed', 'insufficient_data', 'not_recognized', 'attention'] as const) expect(isNotChecked(s)).toBe(false)
  })
  it('filters findings', () => {
    expect(notCheckedFindings([f({ key: 'a', status: 'ok' }), f({ key: 'b', status: 'not_checked' }), f({ key: 'c', status: 'partial' })]).map(x => x.key)).toEqual(['b', 'c'])
  })
})

describe('coverage helpers', () => {
  const cov = {
    sources: [
      { kind: 'live_api', checked: true, partial: true },
      { kind: 'gitops_manifests', checked: false },
      { kind: 'local_files', checked: false },
    ],
  } as unknown as Coverage
  it('lists unchecked and partial sources', () => {
    expect(uncheckedSources(cov).map(s => s.kind)).toEqual(['gitops_manifests', 'local_files'])
    expect(partialSources(cov).map(s => s.kind)).toEqual(['live_api'])
  })
})

describe('findingTitle', () => {
  it('names API, component and fallback', () => {
    expect(findingTitle(f({ api: { apiVersion: 'a/v1', kind: 'K', resourceCount: 1 } }))).toBe('K (a/v1)')
    expect(findingTitle(f({ type: 'component', component: { name: 'CoreDNS', workload: 'coredns' } }))).toBe('CoreDNS · coredns')
    expect(findingTitle(f({ type: 'component', component: { name: 'kubelet', workload: 'kubelet' } }))).toBe('kubelet')
    expect(findingTitle(f({ key: 'platform/x', type: 'platform' }))).toBe('platform/x')
  })
})

describe('messageText', () => {
  it('shows the code when the interface has no translation', () => {
    expect(messageText({ code: 'nope.unknown' })).toBe('nope.unknown')
    expect(messageText(undefined)).toBe('')
  })
})

describe('diffHasChanges', () => {
  const base: Diff = {
    previousId: 'a', currentId: 'b', currentVersionBefore: '1.34', currentVersionAfter: '1.34', targetChanged: false,
    matrixBefore: 'm', matrixAfter: 'm', matrixChanged: false, catalogBefore: 'c', catalogAfter: 'c', catalogChanged: false,
    new: [], resolved: [], changed: [], unchanged: 3,
  }
  it('is false for identical reports and true for any difference', () => {
    expect(diffHasChanges(base)).toBe(false)
    expect(diffHasChanges({ ...base, new: [f({})] })).toBe(true)
    expect(diffHasChanges({ ...base, matrixChanged: true })).toBe(true)
    expect(diffHasChanges({ ...base, targetChanged: true })).toBe(true)
  })
})

describe('safeUrl', () => {
  it('allows only http and https', () => {
    expect(safeUrl('https://kubernetes.io/docs/')).toBe('https://kubernetes.io/docs/')
    expect(safeUrl('javascript:alert(1)')).toBe('')
    expect(safeUrl('file:///etc/passwd')).toBe('')
    expect(safeUrl('not a url')).toBe('')
    expect(safeUrl(undefined)).toBe('')
  })
})
