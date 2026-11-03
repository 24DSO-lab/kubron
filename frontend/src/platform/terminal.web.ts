// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { callApp } from './api'
import type { TerminalHandle, TerminalHandlers, TerminalParams } from './terminal'
import { t } from '../i18n'
import { withBuild } from './build'

/**
 * Веб-терминал: RPC OpenTerminal (сервер выдаёт неугадываемый id и копит вывод) и бинарный
 * WebSocket /api/terminal/{id}. Вывод — бинарные кадры, ввод — бинарные кадры, размер — текстовый
 * JSON {"type":"resize"}. Закрытие кадром 1000 «exit» означает конец процесса (internal/server/terminal_ws.go).
 */
export async function openWebTerminal(p: TerminalParams, h: TerminalHandlers): Promise<TerminalHandle> {
  const id = await callApp('OpenTerminal', p.namespace, p.pod, p.container, p.shell, p.cols, p.rows)
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const url = withBuild(`${proto}://${location.host}/api/terminal/${encodeURIComponent(id)}`)
  const ws = await connectToOwner(url)

  let closedByUs = false
  const encoder = new TextEncoder()
  ws.onmessage = ev => {
    if (ev.data instanceof ArrayBuffer) h.onData(new Uint8Array(ev.data))
  }
  ws.onclose = ev => {
    if (closedByUs) return
    h.onExit(ev.code === 1000 && ev.reason === 'exit' ? 'exit' : 'lost')
  }
  ws.onerror = () => { /* подробностей нет; onclose последует следом */ }

  return {
    write(data) {
      if (ws.readyState === 1) ws.send(encoder.encode(data))
    },
    resize(cols, rows) {
      if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    },
    close() {
      closedByUs = true
      try { ws.close(1000) } catch { /* уже закрыт */ }
      callApp('CloseTerminal', id).catch(() => { }) // завершить процесс в контейнере
    },
  }
}

/** Сколько раз пробуем подключиться: без sticky-affinity запрос может попасть не на реплику-владельца терминала. */
const CONNECT_TRIES = 8

/**
 * Открывает WebSocket терминала. Реплика, которой терминал не принадлежит, отвечает 409 stream_elsewhere
 * (браузер показывает это как обычный сбой подключения), поэтому подключение повторяется: новый запрос
 * балансировщик может направить на владельца.
 */
async function connectToOwner(url: string): Promise<WebSocket> {
  for (let i = 1; ; i++) {
    const ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'
    try {
      await new Promise<void>((resolve, reject) => {
        ws.onopen = () => resolve()
        ws.onerror = () => reject(new Error(t('platform.terminalConnect')))
        ws.onclose = () => reject(new Error(t('platform.terminalConnect')))
      })
      return ws
    } catch (e) {
      if (i >= CONNECT_TRIES) throw e
      await new Promise(r => setTimeout(r, 100 + Math.random() * 200))
    }
  }
}
