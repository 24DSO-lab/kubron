// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenIncidentRequest } from '../types/incident'

// Внешняя зависимость composable — useK8s (тянет Wails, i18n, уведомления). Подменяем её целиком:
// проверяется только логика incidentLaunch, а Monaco и остальной UI-граф в Node-окружение не загружаются.
const { switchView } = vi.hoisted(() => ({ switchView: vi.fn<(view: string) => void>() }))
vi.mock('./useK8s', () => ({ useK8s: () => ({ switchView }) }))

import { investigate, pendingInvestigation, takeInvestigation } from './incidentLaunch'

beforeEach(() => {
  switchView.mockReset()
  pendingInvestigation.value = null
})

describe('investigate', () => {
  it('сохраняет цель расследования', () => {
    investigate('Pod', 'shop', 'api-1')
    expect(pendingInvestigation.value).toEqual<OpenIncidentRequest>({ kind: 'Pod', namespace: 'shop', name: 'api-1', disableLogs: false })
  })

  it('переключает экран на «Расследования» ровно один раз', () => {
    investigate('Pod', 'shop', 'api-1')
    expect(switchView).toHaveBeenCalledTimes(1)
    expect(switchView).toHaveBeenCalledWith('incidents')
  })

  it('цель сохраняется до переключения экрана: экран при монтировании уже видит цель', () => {
    let seen: OpenIncidentRequest | null = null
    switchView.mockImplementation(() => { seen = pendingInvestigation.value })
    investigate('Service', 'shop', 'api')
    expect(seen).toMatchObject({ kind: 'Service', name: 'api' })
  })

  it('у кластерного объекта namespace нормализуется в пустую строку', () => {
    investigate('Node', 'ignored-namespace', 'n1')
    expect(pendingInvestigation.value).toMatchObject({ kind: 'Node', namespace: '', name: 'n1' })
  })

  it('у namespaced-объекта namespace сохраняется как есть', () => {
    investigate('Deployment', 'prod', 'web')
    expect(pendingInvestigation.value?.namespace).toBe('prod')
  })

  it('логи по умолчанию не отключаются: disableLogs=false', () => {
    investigate('Pod', 'shop', 'api-1')
    investigate('Node', '', 'n1')
    expect(pendingInvestigation.value?.disableLogs).toBe(false)
  })

  it('повторный запуск заменяет ожидающую цель', () => {
    investigate('Pod', 'shop', 'a')
    investigate('Pod', 'shop', 'b')
    expect(pendingInvestigation.value?.name).toBe('b')
    expect(switchView).toHaveBeenCalledTimes(2)
  })
})

describe('takeInvestigation', () => {
  it('возвращает цель только один раз', () => {
    investigate('Pod', 'shop', 'api-1')
    expect(takeInvestigation()).toEqual({ kind: 'Pod', namespace: 'shop', name: 'api-1', disableLogs: false })
    expect(pendingInvestigation.value).toBeNull()
  })

  it('повторный вызов возвращает null', () => {
    investigate('Pod', 'shop', 'api-1')
    takeInvestigation()
    expect(takeInvestigation()).toBeNull()
    expect(takeInvestigation()).toBeNull()
  })

  it('без запуска возвращает null и не переключает экран', () => {
    expect(takeInvestigation()).toBeNull()
    expect(switchView).not.toHaveBeenCalled()
  })

  it('не переключает экран сам по себе', () => {
    investigate('Pod', 'shop', 'api-1')
    switchView.mockClear()
    takeInvestigation()
    expect(switchView).not.toHaveBeenCalled()
  })
})
