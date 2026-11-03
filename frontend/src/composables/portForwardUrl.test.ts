// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../platform/api', () => ({ callApp: vi.fn() }))

import { callApp } from '../platform/api'
import { getPortForwardUrl, isProxyForward, stopPortForward } from './portForwardUrl'

describe('getPortForwardUrl', () => {
  beforeEach(() => { vi.mocked(callApp).mockReset() })

  it('берёт адрес у бэкенда и не собирает его из порта', async () => {
    vi.mocked(callApp).mockResolvedValue('https://abc.pf.example.com/__kubron/enter?ticket=t' as never)
    expect(await getPortForwardUrl('abc')).toBe('https://abc.pf.example.com/__kubron/enter?ticket=t')
    expect(callApp).toHaveBeenCalledWith('GetPortForwardURL', 'abc')
  })

  it('для локального TLS-порта меняет схему на https', async () => {
    vi.mocked(callApp).mockResolvedValue('http://localhost:8443' as never)
    expect(await getPortForwardUrl('x', true)).toBe('https://localhost:8443')
    expect(await getPortForwardUrl('x', false)).toBe('http://localhost:8443')
  })
})

describe('isProxyForward', () => {
  it('режим берётся у сессии, а без неё — из возможностей', () => {
    expect(isProxyForward({ mode: 'proxy' })).toBe(true)
    expect(isProxyForward({ mode: 'local' })).toBe(false)
    expect(isProxyForward(null)).toBe(false) // по умолчанию local
  })
})

describe('поток на другой реплике', () => {
  beforeEach(() => { vi.mocked(callApp).mockReset() })

  it('адрес и остановка повторяются, пока запрос не попадёт на владельца', async () => {
    const elsewhere = Object.assign(new Error('stream is on another replica'), { code: 'stream_elsewhere' })
    vi.mocked(callApp)
      .mockRejectedValueOnce(elsewhere as never)
      .mockResolvedValueOnce('https://abc.pf.example.com/__kubron/enter?ticket=t' as never)
    expect(await getPortForwardUrl('abc')).toBe('https://abc.pf.example.com/__kubron/enter?ticket=t')
    expect(callApp).toHaveBeenCalledTimes(2)

    vi.mocked(callApp).mockReset()
    vi.mocked(callApp).mockRejectedValueOnce(elsewhere as never).mockResolvedValueOnce(undefined as never)
    await stopPortForward('abc')
    expect(callApp).toHaveBeenLastCalledWith('StopPortForward', 'abc')
  })

  it('другие ошибки не повторяются', async () => {
    vi.mocked(callApp).mockRejectedValue(new Error('forbidden') as never)
    await expect(stopPortForward('abc')).rejects.toThrow('forbidden')
    expect(callApp).toHaveBeenCalledTimes(1)
  })
})
