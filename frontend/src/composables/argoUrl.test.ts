// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { isValidArgoUrl, MAX_ARGO_URL } from './argoUrl'

describe('isValidArgoUrl', () => {
  it('пусто и http(s) допустимы', () => {
    for (const v of ['', '  ', 'https://argocd.example.com', 'http://argocd.internal:8080/argo']) {
      expect(isValidArgoUrl(v), v).toBe(true)
    }
  })
  it('другие схемы, учётные данные, query и fragment отклоняются', () => {
    for (const v of [
      'javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd', 'argocd.example.com',
      'https://user:pass@argocd.example.com', 'https://argocd.example.com/?a=1', 'https://argocd.example.com/#x',
      'https://argocd.example.com/?', 'https://' + 'a'.repeat(MAX_ARGO_URL),
    ]) {
      expect(isValidArgoUrl(v), v).toBe(false)
    }
  })
})
