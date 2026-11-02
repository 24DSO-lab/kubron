// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { SECRET_PLACEHOLDER, firstPlaceholderRange, hasHiddenSecret } from './kubeconfigSecrets'

describe('kubeconfigSecrets', () => {
  it('распознаёт скрытый секрет', () => {
    expect(hasHiddenSecret(`token: ${SECRET_PLACEHOLDER}`)).toBe(true)
    expect(hasHiddenSecret('token: abc')).toBe(false)
  })

  it('возвращает границы первого плейсхолдера', () => {
    const text = `a: 1\ntoken: ${SECRET_PLACEHOLDER}\npassword: ${SECRET_PLACEHOLDER}`
    const range = firstPlaceholderRange(text)
    expect(range).not.toBeNull()
    const [from, to] = range as [number, number]
    expect(text.slice(from, to)).toBe(SECRET_PLACEHOLDER)
    expect(from).toBe(text.indexOf('token: ') + 'token: '.length)
  })

  it('без плейсхолдера границ нет', () => {
    expect(firstPlaceholderRange('token: abc')).toBeNull()
  })

  it('совпадает с серверным значением', () => {
    expect(SECRET_PLACEHOLDER).toBe('<kubron:unchanged>')
  })
})
