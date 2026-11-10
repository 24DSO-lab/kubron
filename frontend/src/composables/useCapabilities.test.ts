// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../platform/api', () => ({ callApp: vi.fn() }))

import { callApp } from '../platform/api'
import {
  DEFAULT_CAPABILITIES, hasCapability, loadCapabilities, normalizeCapabilities, useCapabilities,
} from './useCapabilities'

describe('normalizeCapabilities', () => {
  it('пустой или некорректный ответ даёт значения по умолчанию', () => {
    expect(normalizeCapabilities(undefined)).toEqual(DEFAULT_CAPABILITIES)
    expect(normalizeCapabilities(null)).toEqual(DEFAULT_CAPABILITIES)
    expect(normalizeCapabilities('x')).toEqual(DEFAULT_CAPABILITIES)
  })
  it('берёт только булевы известные флаги', () => {
    const c = normalizeCapabilities({ nodeShell: false, portForward: 'no', unknown: true, sso: true })
    expect(c.nodeShell).toBe(false)
    expect(c.portForward).toBe(DEFAULT_CAPABILITIES.portForward)
    expect(c.sso).toBe(true)
    expect('unknown' in c).toBe(false)
  })
  it('режим port-forward берётся только из известных значений', () => {
    expect(normalizeCapabilities({ portForwardMode: 'proxy' }).portForwardMode).toBe('proxy')
    expect(normalizeCapabilities({ portForwardMode: 'off' }).portForwardMode).toBe('off')
    expect(normalizeCapabilities({ portForwardMode: 'x' }).portForwardMode).toBe('local')
    expect(normalizeCapabilities({ portForwardMode: true }).portForwardMode).toBe('local')
  })
  it('по умолчанию десктопное поведение: всё локальное доступно, SSO нет', () => {
    expect(DEFAULT_CAPABILITIES).toEqual({
      fileDialog: true, portForward: true, portForwardMode: 'local', exec: true, nodeShell: true, debug: true, localKubeconfig: true, sso: false, inCluster: false, teams: false, bugReport: false,
    })
  })

  it('кнопка «Сообщить об ошибке» выключена, пока бэкенд её не включил', () => {
    expect(normalizeCapabilities({}).bugReport).toBe(false)
    expect(normalizeCapabilities({ bugReport: true }).bugReport).toBe(true)
    expect(normalizeCapabilities({ bugReport: 'yes' }).bugReport).toBe(false)
  })
})

describe('loadCapabilities', () => {
  beforeEach(() => { vi.mocked(callApp).mockReset() })

  it('применяет ответ бэкенда', async () => {
    vi.mocked(callApp).mockResolvedValue({
      fileDialog: false, portForward: true, nodeShell: false, localKubeconfig: false, sso: true,
    } as never)
    await loadCapabilities()
    const { capabilities, loaded } = useCapabilities()
    expect(capabilities.value.fileDialog).toBe(false)
    expect(capabilities.value.sso).toBe(true)
    expect(loaded.value).toBe(true)
  })

  it('hasCapability читает флаги, отключённые администратором', async () => {
    vi.mocked(callApp).mockResolvedValue({ exec: false, nodeShell: false } as never)
    await loadCapabilities()
    expect(hasCapability('exec')).toBe(false)
    expect(hasCapability('nodeShell')).toBe(false)
  })

  it('при ошибке возвращает значения по умолчанию и не бросает', async () => {
    vi.mocked(callApp).mockImplementation(() => Promise.reject(new Error('нет метода')))
    await expect(loadCapabilities()).resolves.toBeUndefined()
    expect({ ...useCapabilities().capabilities.value }).toEqual({ ...DEFAULT_CAPABILITIES })
  })
})
