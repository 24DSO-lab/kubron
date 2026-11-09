// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyImageToClipboard } from './clipboardImage'

const PNG = 'data:image/png;base64,' + btoa('png')

class FakeItem {
  constructor(public items: Record<string, Blob>) {}
}

afterEach(() => { vi.unstubAllGlobals() })

describe('copyImageToClipboard', () => {
  it('кладёт PNG в буфер обмена и сообщает об успехе', async () => {
    const write = vi.fn(async () => {})
    vi.stubGlobal('ClipboardItem', FakeItem)
    vi.stubGlobal('navigator', { clipboard: { write } })
    expect(await copyImageToClipboard(PNG)).toBe(true)
    const [items] = write.mock.calls[0] as unknown as [FakeItem[]]
    expect(items[0].items['image/png'].type).toBe('image/png')
  })

  it('без поддержки веб-вью или при отказе — false, исключений нет', async () => {
    vi.stubGlobal('navigator', {})
    expect(await copyImageToClipboard(PNG)).toBe(false)
    vi.stubGlobal('ClipboardItem', FakeItem)
    vi.stubGlobal('navigator', { clipboard: { write: vi.fn(async () => { throw new Error('denied') }) } })
    expect(await copyImageToClipboard(PNG)).toBe(false)
  })

  it('не PNG в буфер не идёт', async () => {
    const write = vi.fn(async () => {})
    vi.stubGlobal('ClipboardItem', FakeItem)
    vi.stubGlobal('navigator', { clipboard: { write } })
    expect(await copyImageToClipboard('data:text/plain;base64,QUJD')).toBe(false)
    expect(write).not.toHaveBeenCalled()
  })
})
