// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { displayVersion, normalizeInfo, shouldShowBanner } from './useUpdates'

describe('displayVersion', () => {
  it('добавляет v к номеру версии', () => expect(displayVersion('1.2.0')).toBe('v1.2.0'))
  it('оставляет dev как есть', () => expect(displayVersion('dev')).toBe('dev'))
  it('пусто для пустого значения', () => {
    expect(displayVersion('')).toBe('')
    expect(displayVersion(undefined)).toBe('')
  })
})

describe('normalizeInfo', () => {
  it('отвергает не-объекты', () => {
    expect(normalizeInfo(null)).toBeNull()
    expect(normalizeInfo('x')).toBeNull()
  })

  it('подставляет значения по умолчанию', () => {
    const i = normalizeInfo({ current: '1.0.0' })!
    expect(i.install.method).toBe('unknown')
    expect(i.commands).toEqual([])
    expect(i.available).toBe(false)
  })

  it('не считает обновление доступным без номера новой версии', () => {
    expect(normalizeInfo({ current: '1.0.0', available: true })!.available).toBe(false)
  })

  it('сохраняет команды и способ установки', () => {
    const i = normalizeInfo({
      current: '1.0.0', latest: '1.1.0', available: true,
      install: { target: 'web', method: 'helm', os: 'linux', arch: 'amd64' },
      commands: [{ id: 'helmUpgrade', text: 'helm upgrade kubron' }],
    })!
    expect(i.available).toBe(true)
    expect(i.install.method).toBe('helm')
    expect(i.commands).toHaveLength(1)
  })
})

describe('shouldShowBanner', () => {
  const info = normalizeInfo({ current: '1.0.0', latest: '1.1.0', available: true })
  it('показывается, пока версию не скрыли', () => expect(shouldShowBanner(info, '')).toBe(true))
  it('скрыт для скрытой версии', () => expect(shouldShowBanner(info, '1.1.0')).toBe(false))
  it('появляется снова для следующей версии', () => expect(shouldShowBanner(info, '1.0.5')).toBe(true))
  it('не показывается без обновления', () => {
    expect(shouldShowBanner(normalizeInfo({ current: '1.0.0' }), '')).toBe(false)
    expect(shouldShowBanner(null, '')).toBe(false)
  })
})
