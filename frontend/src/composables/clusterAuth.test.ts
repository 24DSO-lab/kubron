// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { canShareAccount, isSharedAccount, signsIn } from './clusterAuth'

describe('signsIn', () => {
  it('статические данные и старый бэкенд без поля — без входа', () => {
    expect(signsIn('static')).toBe(false)
    expect(signsIn(undefined)).toBe(false)
    expect(signsIn('')).toBe(false)
  })
  it('oidc, облачные и прочие плагины входят через брокера', () => {
    for (const a of ['oidc', 'cloud-eks', 'cloud-gke', 'cloud-aks', 'exec']) expect(signsIn(a)).toBe(true)
  })
})

describe('canShareAccount', () => {
  it('нужны и команда, и вход через провайдера', () => {
    expect(canShareAccount('oidc', true)).toBe(true)
    expect(canShareAccount('oidc', false)).toBe(false)
    expect(canShareAccount('static', true)).toBe(false)
  })
})

describe('isSharedAccount', () => {
  it('только режим общей учётной записи', () => {
    expect(isSharedAccount({ mode: 'oidc-password' })).toBe(true)
    expect(isSharedAccount({ mode: 'in-cluster' })).toBe(false)
    expect(isSharedAccount({})).toBe(false)
  })
})
