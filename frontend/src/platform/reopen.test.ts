// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { ReopenPolicy } from './reopen'

describe('ReopenPolicy', () => {
  it('разрешает не больше max попыток подряд', () => {
    let t = 0
    const p = new ReopenPolicy(3, 30_000, () => t)
    p.opened()
    expect([p.allow(), p.allow(), p.allow(), p.allow()]).toEqual([true, true, true, false])
  })

  it('после стабильной работы счётчик сбрасывается', () => {
    let t = 1_000
    const p = new ReopenPolicy(2, 30_000, () => t)
    p.opened()
    expect([p.allow(), p.allow(), p.allow()]).toEqual([true, true, false])
    t += 31_000 // терминал открылся и проработал дольше healthyMs
    p.opened()
    t += 31_000
    expect(p.allow()).toBe(true)
  })

  it('быстрые потери подряд не сбрасывают счётчик', () => {
    let t = 0
    const p = new ReopenPolicy(2, 30_000, () => t)
    for (let i = 0; i < 2; i++) { p.opened(); t += 1_000; expect(p.allow()).toBe(true) }
    p.opened(); t += 1_000
    expect(p.allow()).toBe(false)
  })
})
