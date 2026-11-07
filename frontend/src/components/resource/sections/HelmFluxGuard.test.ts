// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { helmFluxOwner, withFluxWarning } from './HelmFluxGuard'

const managed = { name: 'podinfo', namespace: 'apps', fluxManagedBy: { kind: 'HelmRelease', namespace: 'flux-system', name: 'podinfo-hr' } }
const plain = { name: 'redis', namespace: 'apps', fluxManagedBy: null }

describe('helmFluxOwner: кто управляет релизом', () => {
  it('отдаёт namespace и имя HelmRelease', () => {
    expect(helmFluxOwner(managed)).toEqual({ namespace: 'flux-system', name: 'podinfo-hr' })
  })

  it('релиз без Flux, старый ответ без поля и пустые значения — null', () => {
    expect(helmFluxOwner(plain)).toBeNull()
    expect(helmFluxOwner({ name: 'a' })).toBeNull()
    expect(helmFluxOwner(undefined)).toBeNull()
    expect(helmFluxOwner(null)).toBeNull()
    expect(helmFluxOwner({ fluxManagedBy: {} })).toBeNull()
    expect(helmFluxOwner({ fluxManagedBy: { namespace: 'x' } })).toBeNull()
    expect(helmFluxOwner({ fluxManagedBy: 'HelmRelease' })).toBeNull()
  })
})

describe('withFluxWarning: предупреждение перед ручным действием', () => {
  const base = 'Roll back podinfo to revision #3?'

  it('релиз не под Flux: текст подтверждения не меняется', () => {
    for (const action of ['upgrade', 'rollback', 'uninstall'] as const) {
      expect(withFluxWarning(action, plain, 'redis', base)).toBe(base)
    }
  })

  it('релиз под Flux: перед исходным вопросом стоит предупреждение с именами релиза и HelmRelease', () => {
    const text = withFluxWarning('rollback', managed, 'podinfo', base)
    expect(text).toContain('Flux')
    expect(text).toContain('podinfo')
    expect(text).toContain('flux-system/podinfo-hr')
    expect(text.endsWith(base)).toBe(true)
    expect(text.length).toBeGreaterThan(base.length)
  })

  it('для каждого действия свой текст, оба вопроса сохраняются', () => {
    const texts = (['upgrade', 'rollback', 'uninstall'] as const).map(a => withFluxWarning(a, managed, 'podinfo', base))
    expect(new Set(texts).size).toBe(3)
    for (const text of texts) expect(text.endsWith(base)).toBe(true)
  })

  it('подставляет имя из аргумента, а не из ресурса: вкладка знает имя точно', () => {
    expect(withFluxWarning('upgrade', managed, 'custom-name', base)).toContain('custom-name')
  })
})
