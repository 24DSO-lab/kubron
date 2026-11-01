// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'
import { retryElsewhere, STREAM_ELSEWHERE } from './streams'

const elsewhere = () => Object.assign(new Error('stream is on another replica'), { code: STREAM_ELSEWHERE })

describe('retryElsewhere', () => {
  it('повторяет, пока запрос не попадёт на владельца', async () => {
    const fn = vi.fn().mockRejectedValueOnce(elsewhere()).mockRejectedValueOnce(elsewhere()).mockResolvedValue('ok')
    await expect(retryElsewhere(fn, 8, () => 0)).resolves.toBe('ok')
    expect(fn).toHaveBeenCalledTimes(3)
  })

  it('не повторяет другие ошибки', async () => {
    const fn = vi.fn().mockRejectedValue(new Error('boom'))
    await expect(retryElsewhere(fn, 8, () => 0)).rejects.toThrow('boom')
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('сдаётся после лимита попыток', async () => {
    const fn = vi.fn().mockRejectedValue(elsewhere())
    await expect(retryElsewhere(fn, 3, () => 0)).rejects.toMatchObject({ code: STREAM_ELSEWHERE })
    expect(fn).toHaveBeenCalledTimes(3)
  })
})
