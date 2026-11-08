// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import type { FluxObjectInfo, FluxRef, FluxState } from '../types/flux'
import {
  chartLabel,
  dependsOnLabel,
  fluxStateRank,
  problemRank,
  shortRevision,
  sourceRefLabel,
  summarizeFlux,
} from './flux'

const SHA1 = '0123456789abcdef0123456789abcdef01234567'
const SHA256 = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'

type Item = Pick<FluxObjectInfo, 'name' | 'namespace' | 'state' | 'sourceRef' | 'lastReconcileAt'>

/** Минимальная заглушка объекта: тесты задают только значимые поля */
const obj = (p: Partial<Item>): Item => ({
  name: 'app',
  namespace: 'flux-system',
  state: 'Ready',
  sourceRef: null,
  lastReconcileAt: '',
  ...p,
})

const ref = (p: Partial<FluxRef>): FluxRef => ({ kind: 'GitRepository', namespace: '', name: 'repo', ...p })

describe('fluxStateRank', () => {
  it('Ready = 0, остальные состояния больше нуля', () => {
    expect(fluxStateRank('Ready')).toBe(0)
    for (const s of ['Suspended', 'Stalled', 'Failed', 'Reconciling', 'Unknown'] as FluxState[]) {
      expect(fluxStateRank(s)).toBeGreaterThan(0)
    }
  })

  it('Failed и Stalled выше Reconciling и Suspended', () => {
    for (const bad of ['Failed', 'Stalled'] as FluxState[]) {
      expect(fluxStateRank(bad)).toBeGreaterThan(fluxStateRank('Reconciling'))
      expect(fluxStateRank(bad)).toBeGreaterThan(fluxStateRank('Suspended'))
    }
  })

  it('неизвестное и пустое состояние ранжируется как Unknown, без исключений', () => {
    expect(fluxStateRank('Exploded')).toBe(fluxStateRank('Unknown'))
    expect(fluxStateRank('')).toBe(fluxStateRank('Unknown'))
    expect(fluxStateRank(undefined)).toBe(fluxStateRank('Unknown'))
    expect(fluxStateRank(null)).toBe(fluxStateRank('Unknown'))
  })
})

describe('problemRank', () => {
  it('равен рангу состояния объекта', () => {
    expect(problemRank(obj({ state: 'Failed' }))).toBe(fluxStateRank('Failed'))
    expect(problemRank(obj({ state: 'Ready' }))).toBe(0)
  })
})

describe('shortRevision', () => {
  it('пусто и undefined дают пустую строку', () => {
    expect(shortRevision('')).toBe('')
    expect(shortRevision(undefined)).toBe('')
    expect(shortRevision('   ')).toBe('')
  })

  it('ветка@sha1:<40 hex> — ветка целиком, хэш 7 символов', () => {
    expect(shortRevision(`main@sha1:${SHA1}`)).toBe('main@0123456')
    expect(shortRevision(`release/1.x@sha1:${SHA1}`)).toBe('release/1.x@0123456')
  })

  it('ветка@sha256:<64 hex> (OCI и новые репозитории)', () => {
    expect(shortRevision(`latest@sha256:${SHA256}`)).toBe('latest@0123456')
  })

  it('чистый дайджест sha256:<64 hex> сохраняет алгоритм', () => {
    expect(shortRevision(`sha256:${SHA256}`)).toBe('sha256:0123456')
  })

  it('голый sha1 в 40 символов сокращается до 7', () => {
    expect(shortRevision(SHA1)).toBe('0123456')
    expect(shortRevision(SHA1.toUpperCase())).toBe('0123456'.toUpperCase())
  })

  it('теги и версии остаются целиком', () => {
    expect(shortRevision('v1.2.3')).toBe('v1.2.3')
    expect(shortRevision('1.2.3-rc.1+build.5')).toBe('1.2.3-rc.1+build.5')
    expect(shortRevision('latest')).toBe('latest')
  })

  it('короткие hex-слова и хэши неверной длины не режутся', () => {
    expect(shortRevision('deadbeef')).toBe('deadbeef')
    expect(shortRevision('main@sha1:abc123')).toBe('main@sha1:abc123')
    expect(shortRevision(`sha256:${SHA1}`)).toBe(`sha256:${SHA1}`)
  })

  it('тег с @ без хэша не ломается', () => {
    expect(shortRevision('foo@bar')).toBe('foo@bar')
  })
})

describe('sourceRefLabel и dependsOnLabel', () => {
  it('источник: Kind/name, с namespace — Kind/ns/name', () => {
    expect(sourceRefLabel(ref({}))).toBe('GitRepository/repo')
    expect(sourceRefLabel(ref({ namespace: 'infra' }))).toBe('GitRepository/infra/repo')
  })

  it('пустая или неполная ссылка дают пустую строку', () => {
    expect(sourceRefLabel(null)).toBe('')
    expect(sourceRefLabel(undefined)).toBe('')
    expect(sourceRefLabel(ref({ name: '' }))).toBe('')
  })

  it('источник без вида: только имя', () => {
    expect(sourceRefLabel(ref({ kind: '' }))).toBe('repo')
  })

  it('dependsOn: вид не указывается — namespace/name или имя', () => {
    expect(dependsOnLabel({ kind: '', namespace: 'infra', name: 'base' })).toBe('infra/base')
    expect(dependsOnLabel({ kind: '', namespace: '', name: 'base' })).toBe('base')
    expect(dependsOnLabel({ kind: 'Kustomization', namespace: '', name: 'base' })).toBe('Kustomization/base')
  })

  it('dependsOn: пустая ссылка', () => {
    expect(dependsOnLabel(null)).toBe('')
    expect(dependsOnLabel({ kind: '', namespace: '', name: '' })).toBe('')
  })
})

describe('chartLabel', () => {
  it('имя@версия', () => {
    expect(chartLabel('podinfo', '6.5.0')).toBe('podinfo@6.5.0')
  })
  it('без версии — только имя, без имени — пусто', () => {
    expect(chartLabel('podinfo', '')).toBe('podinfo')
    expect(chartLabel('podinfo', undefined)).toBe('podinfo')
    expect(chartLabel('', '6.5.0')).toBe('')
    expect(chartLabel(undefined, undefined)).toBe('')
  })
})

describe('summarizeFlux', () => {
  it('пустой ввод даёт нули и пустые списки', () => {
    const s = summarizeFlux([])
    expect(s).toEqual({
      total: 0, ready: 0, reconciling: 0, failed: 0, stalled: 0, suspended: 0, unknown: 0,
      problems: [], byNamespace: [], bySource: [], recent: [],
    })
  })

  it('null и undefined вместо списка не падают', () => {
    expect(summarizeFlux(null).total).toBe(0)
    expect(summarizeFlux(undefined).total).toBe(0)
  })

  it('счётчики по состояниям; неизвестное состояние попадает в unknown', () => {
    const items = [
      obj({ name: 'a', state: 'Ready' }),
      obj({ name: 'b', state: 'Ready' }),
      obj({ name: 'c', state: 'Failed' }),
      obj({ name: 'd', state: 'Stalled' }),
      obj({ name: 'e', state: 'Reconciling' }),
      obj({ name: 'f', state: 'Suspended' }),
      obj({ name: 'g', state: 'Unknown' }),
      obj({ name: 'h', state: 'Weird' as FluxState }),
    ]
    const s = summarizeFlux(items)
    expect(s.total).toBe(8)
    expect(s.ready).toBe(2)
    expect(s.failed).toBe(1)
    expect(s.stalled).toBe(1)
    expect(s.reconciling).toBe(1)
    expect(s.suspended).toBe(1)
    expect(s.unknown).toBe(2)
  })

  it('проблемные: без Ready, по убыванию ранга, при равенстве по namespace и имени', () => {
    const items = [
      obj({ name: 'z-ready', state: 'Ready' }),
      obj({ name: 'b-susp', state: 'Suspended' }),
      obj({ name: 'b-fail', state: 'Failed', namespace: 'b' }),
      obj({ name: 'a-fail', state: 'Failed', namespace: 'a' }),
      obj({ name: 'x-stall', state: 'Stalled' }),
      obj({ name: 'r', state: 'Reconciling' }),
    ]
    const names = summarizeFlux(items).problems.map(p => p.name)
    expect(names).toEqual(['x-stall', 'a-fail', 'b-fail', 'r', 'b-susp'])
  })

  it('problems сохраняет исходные объекты (тип расширенного DTO)', () => {
    const k = { ...obj({ state: 'Failed' }), path: './apps' }
    expect(summarizeFlux([k]).problems[0].path).toBe('./apps')
  })

  it('разрез по namespace: проблемные первыми, затем по числу объектов, затем по имени', () => {
    const items = [
      obj({ namespace: 'a', state: 'Ready' }),
      obj({ namespace: 'a', state: 'Ready' }),
      obj({ namespace: 'a', state: 'Ready' }),
      obj({ namespace: 'b', state: 'Failed' }),
      obj({ namespace: 'c', state: 'Ready' }),
    ]
    expect(summarizeFlux(items).byNamespace).toEqual([
      { name: 'b', total: 1, problems: 1 },
      { name: 'a', total: 3, problems: 0 },
      { name: 'c', total: 1, problems: 0 },
    ])
  })

  it('разрез по источнику: пустой namespace ссылки берётся из объекта; без источника — пустое имя', () => {
    const items = [
      obj({ namespace: 'a', sourceRef: ref({ name: 'repo' }), state: 'Ready' }),
      obj({ namespace: 'a', sourceRef: ref({ name: 'repo' }), state: 'Failed' }),
      obj({ namespace: 'b', sourceRef: ref({ name: 'repo' }), state: 'Ready' }),
      obj({ namespace: 'b', sourceRef: ref({ name: 'repo', namespace: 'a' }), state: 'Ready' }),
      obj({ namespace: 'b', sourceRef: null, state: 'Ready' }),
    ]
    const by = summarizeFlux(items).bySource
    expect(by).toEqual([
      { name: 'GitRepository/a/repo', total: 3, problems: 1 },
      { name: '', total: 1, problems: 0 },
      { name: 'GitRepository/b/repo', total: 1, problems: 0 },
    ])
  })

  it('последние reconcile: новые первыми, без пустых и некорректных дат, с лимитом', () => {
    const items = [
      obj({ name: 'old', lastReconcileAt: '2026-11-01T10:00:00Z' }),
      obj({ name: 'new', lastReconcileAt: '2026-11-07T10:00:00Z' }),
      obj({ name: 'none', lastReconcileAt: '' }),
      obj({ name: 'bad', lastReconcileAt: 'вчера' }),
      obj({ name: 'mid', lastReconcileAt: '2026-11-05T10:00:00Z' }),
    ]
    expect(summarizeFlux(items).recent.map(i => i.name)).toEqual(['new', 'mid', 'old'])
    expect(summarizeFlux(items, 2).recent.map(i => i.name)).toEqual(['new', 'mid'])
    expect(summarizeFlux(items, 0).recent).toEqual([])
  })

  it('не мутирует входной массив', () => {
    const items = [obj({ name: 'b', state: 'Ready' }), obj({ name: 'a', state: 'Failed' })]
    const copy = [...items]
    summarizeFlux(items)
    expect(items).toEqual(copy)
  })
})
