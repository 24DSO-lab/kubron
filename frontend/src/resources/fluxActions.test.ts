// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Язык не влияет на тесты: t() возвращает ключ и параметры
vi.mock('../i18n', () => ({ t: (k: string, p?: Record<string, unknown>) => (p ? `${k}${JSON.stringify(p)}` : k) }))
vi.mock('./wails', () => ({ callApp: vi.fn() }))
vi.mock('./confirm', () => ({ askConfirm: vi.fn(async () => true) }))
vi.mock('./store', () => ({ reloadActive: vi.fn(async () => {}) }))
const notifySuccess = vi.fn()
const notifyError = vi.fn()
vi.mock('../composables/useNotifications', () => ({ useNotifications: () => ({ notifySuccess, notifyError }) }))
vi.mock('../composables/errorText', () => ({ errorText: (e: unknown) => String((e as Error)?.message ?? e) }))

import { callApp } from './wails'
import { askConfirm } from './confirm'
import { reloadActive } from './store'
import type { FluxActionResult, FluxHelmReleaseInfo, FluxKustomizationInfo, FluxObjectInfo } from '../types/flux'
import {
  deleteConfirmed, deleteConsequence, deleteConsequenceText, deleteFluxObject, fluxActionTick, reconcileFlux,
  refOf, reportFlux, setFluxSuspended, suspendQuestion,
} from './fluxActions'

const call = vi.mocked(callApp) as unknown as ReturnType<typeof vi.fn>
const obj = (name: string, p: Record<string, unknown> = {}) =>
  ({ kind: 'Kustomization', namespace: 'flux-system', name, ...p }) as unknown as FluxObjectInfo
const ok = (name: string): FluxActionResult => ({ kind: 'Kustomization', namespace: 'flux-system', name, error: '' })
const bad = (name: string, error = 'denied'): FluxActionResult => ({ kind: 'Kustomization', namespace: 'flux-system', name, error })

beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  vi.mocked(askConfirm).mockResolvedValue(true)
  fluxActionTick.value = 0
})
afterEach(() => { vi.useRealTimers() })

describe('refOf', () => {
  it('берёт только вид, namespace и имя', () => {
    expect(refOf(obj('a', { secret: 'x' }))).toEqual({ kind: 'Kustomization', namespace: 'flux-system', name: 'a' })
  })
})

describe('reportFlux', () => {
  it('один успех — тост с именем', () => {
    expect(reportFlux([obj('a')], [ok('a')], 'done')).toBe(true)
    expect(notifySuccess).toHaveBeenCalledTimes(1)
    expect(notifySuccess.mock.calls[0][0]).toContain('fluxActions.doneOne')
    expect(notifyError).not.toHaveBeenCalled()
  })

  it('несколько успехов — один сводный тост', () => {
    expect(reportFlux([obj('a'), obj('b')], [ok('a'), ok('b')], 'done')).toBe(true)
    expect(notifySuccess.mock.calls[0][0]).toContain('fluxActions.doneMany')
    expect(notifySuccess.mock.calls[0][0]).toContain('"ok":2')
  })

  it('частичный отказ: успех и ошибка по каждому объекту, результат false', () => {
    expect(reportFlux([obj('a'), obj('b')], [ok('a'), bad('b', 'forbidden')], 'done')).toBe(false)
    expect(notifySuccess).toHaveBeenCalledTimes(1)
    expect(notifyError).toHaveBeenCalledTimes(1)
    expect(notifyError.mock.calls[0][0]).toContain('forbidden')
    expect(notifyError.mock.calls[0][0]).toContain('"name":"b"')
  })

  it('больше трёх ошибок: три тоста и счётчик остальных', () => {
    const items = ['a', 'b', 'c', 'd', 'e'].map(n => obj(n))
    expect(reportFlux(items, items.map(i => bad(i.name)), 'done')).toBe(false)
    expect(notifySuccess).not.toHaveBeenCalled()
    expect(notifyError).toHaveBeenCalledTimes(4)
    expect(notifyError.mock.calls[3][0]).toContain('fluxActions.moreFailed')
    expect(notifyError.mock.calls[3][0]).toContain('"n":2')
  })

  it('пустой, null и неполный ответ: объекты без результата считаются невыполненными', () => {
    expect(reportFlux([obj('a')], null, 'done')).toBe(false)
    expect(reportFlux([obj('a'), obj('b')], [ok('a')], 'done')).toBe(false)
    expect(notifyError.mock.calls.some(c => String(c[0]).includes('fluxActions.noResult'))).toBe(true)
  })
})

describe('reconcileFlux', () => {
  it('вызывает ReconcileFlux со ссылками и withSource, затем обновляет сразу и через 3 с', async () => {
    call.mockResolvedValue([ok('a')])
    await reconcileFlux([obj('a')], { withSource: true })
    expect(call).toHaveBeenCalledWith('ReconcileFlux', [{ kind: 'Kustomization', namespace: 'flux-system', name: 'a' }], true)
    expect(askConfirm).not.toHaveBeenCalled()
    expect(fluxActionTick.value).toBe(1)
    expect(reloadActive).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(3000)
    expect(fluxActionTick.value).toBe(2)
    expect(reloadActive).toHaveBeenCalledTimes(2)
  })

  it('пустой список — ни вызова, ни обновления', async () => {
    await reconcileFlux([], { withSource: false })
    expect(call).not.toHaveBeenCalled()
    expect(fluxActionTick.value).toBe(0)
  })

  it('ошибка вызова — тост, исключения нет, обновления нет', async () => {
    call.mockRejectedValue(new Error('boom'))
    await expect(reconcileFlux([obj('a')], { withSource: false })).resolves.toBeUndefined()
    expect(notifyError.mock.calls[0][0]).toContain('boom')
    expect(fluxActionTick.value).toBe(0)
  })
})

describe('setFluxSuspended', () => {
  it('suspend: подтверждение с предупреждением про Git, затем SuspendFlux', async () => {
    call.mockResolvedValue([ok('a')])
    await setFluxSuspended([obj('a')], true)
    expect(askConfirm).toHaveBeenCalledTimes(1)
    expect(vi.mocked(askConfirm).mock.calls[0][0]).toContain('fluxActions.suspendConfirmOne')
    expect(call).toHaveBeenCalledWith('SuspendFlux', [{ kind: 'Kustomization', namespace: 'flux-system', name: 'a' }])
    expect(fluxActionTick.value).toBe(1)
  })

  it('resume: подтверждение и ResumeFlux', async () => {
    call.mockResolvedValue([ok('a'), ok('b')])
    await setFluxSuspended([obj('a'), obj('b')], false)
    expect(vi.mocked(askConfirm).mock.calls[0][0]).toContain('fluxActions.resumeConfirmMany')
    expect(call).toHaveBeenCalledWith('ResumeFlux', expect.any(Array))
  })

  it('отказ в подтверждении — вызова нет', async () => {
    vi.mocked(askConfirm).mockResolvedValue(false)
    await setFluxSuspended([obj('a')], true)
    expect(call).not.toHaveBeenCalled()
    expect(fluxActionTick.value).toBe(0)
  })

  it('частичный отказ разбирается по объектам', async () => {
    call.mockResolvedValue([ok('a'), bad('b')])
    await setFluxSuspended([obj('a'), obj('b')], true)
    expect(notifySuccess).toHaveBeenCalledTimes(1)
    expect(notifyError).toHaveBeenCalledTimes(1)
  })

  it('текст вопроса: один объект и несколько', () => {
    expect(suspendQuestion([obj('a')], true)).toContain('suspendConfirmOne')
    expect(suspendQuestion([obj('a'), obj('b')], true)).toContain('suspendConfirmMany')
    expect(suspendQuestion([obj('a')], false)).toContain('resumeConfirmOne')
  })
})

describe('последствия удаления', () => {
  const ks = (p: Partial<FluxKustomizationInfo>) => ({ kind: 'Kustomization', ...p }) as Partial<FluxKustomizationInfo>
  it('Kustomization с prune=true: управляемые ресурсы удаляются, показано число из inventory', () => {
    const c = deleteConsequence(ks({ prune: true, inventoryCount: 12 }))
    expect(c).toEqual({ kind: 'prune', count: 12 })
    expect(deleteConsequenceText(c)).toEqual({ key: 'fluxActions.delete.prune', params: { n: 12 } })
  })
  it('prune=true без inventory — число 0, не NaN', () => {
    expect(deleteConsequence(ks({ prune: true }))).toEqual({ kind: 'prune', count: 0 })
    expect(deleteConsequence(ks({ prune: true, inventoryCount: -5 }))).toEqual({ kind: 'prune', count: 0 })
  })
  it('Kustomization с prune=false или без поля: ресурсы остаются без управления', () => {
    expect(deleteConsequence(ks({ prune: false }))).toEqual({ kind: 'keep' })
    expect(deleteConsequence(ks({}))).toEqual({ kind: 'keep' })
    expect(deleteConsequenceText({ kind: 'keep' })!.key).toBe('fluxActions.delete.keep')
  })
  it('HelmRelease: uninstall вместе с ресурсами', () => {
    const c = deleteConsequence({ kind: 'HelmRelease' } as Partial<FluxHelmReleaseInfo>)
    expect(c).toEqual({ kind: 'helmUninstall' })
    expect(deleteConsequenceText(c)!.key).toBe('fluxActions.delete.helmUninstall')
  })
  it('неизвестный вид — без специального последствия, без исключения', () => {
    expect(deleteConsequence({ kind: 'Bucket' } as any)).toEqual({ kind: 'none' })
    expect(deleteConsequenceText({ kind: 'none' })).toBeNull()
    expect(() => deleteConsequence({})).not.toThrow()
  })
})

describe('deleteConfirmed', () => {
  it('только точное совпадение имени', () => {
    expect(deleteConfirmed('apps', 'apps')).toBe(true)
    expect(deleteConfirmed('app', 'apps')).toBe(false)
    expect(deleteConfirmed('Apps', 'apps')).toBe(false)
    expect(deleteConfirmed(' apps', 'apps')).toBe(false)
    expect(deleteConfirmed('apps ', 'apps')).toBe(false)
    expect(deleteConfirmed('', 'apps')).toBe(false)
  })
  it('пустое имя объекта никогда не подтверждается', () => {
    expect(deleteConfirmed('', '')).toBe(false)
  })
})

describe('deleteFluxObject', () => {
  it('успех: DeleteFlux с одной ссылкой, тост, обновление; true', async () => {
    call.mockResolvedValue(undefined)
    const o = obj('apps') as FluxKustomizationInfo
    expect(await deleteFluxObject(o)).toBe(true)
    expect(call).toHaveBeenCalledWith('DeleteFlux', { kind: 'Kustomization', namespace: 'flux-system', name: 'apps' })
    expect(notifySuccess).toHaveBeenCalledTimes(1)
    expect(fluxActionTick.value).toBe(1)
  })
  it('ошибка: тост, false, обновления нет', async () => {
    call.mockRejectedValue(new Error('forbidden'))
    expect(await deleteFluxObject(obj('apps') as FluxKustomizationInfo)).toBe(false)
    expect(notifyError.mock.calls[0][0]).toContain('forbidden')
    expect(fluxActionTick.value).toBe(0)
  })
})
