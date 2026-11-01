// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import DefaultTheme from 'vitepress/theme'
import './custom.css'

export default {
  ...DefaultTheme,
  enhanceApp() {
    if (typeof window === 'undefined') return

    document.addEventListener('click', (event) => {
      const target = event.target
      if (!(target instanceof HTMLImageElement)) return
      if (!target.closest('.screenshot-grid')) return

      let dialog = document.querySelector<HTMLDialogElement>('.screenshot-lightbox')
      if (!dialog) {
        dialog = document.createElement('dialog')
        dialog.className = 'screenshot-lightbox'

        const close = document.createElement('button')
        close.type = 'button'
        close.setAttribute('aria-label', 'Close enlarged screenshot')
        close.textContent = '×'
        close.addEventListener('click', () => dialog?.close())

        const image = document.createElement('img')
        const caption = document.createElement('p')
        dialog.append(close, image, caption)
        dialog.addEventListener('click', (clickEvent) => {
          if (clickEvent.target === dialog) dialog?.close()
        })
        document.body.append(dialog)
      }

      const image = dialog.querySelector('img')
      const caption = dialog.querySelector('p')
      if (!image || !caption) return

      image.src = target.currentSrc || target.src
      image.alt = target.alt
      caption.textContent = target.closest('figure')?.querySelector('figcaption')?.textContent || target.alt
      dialog.showModal()
    })
  }
}
