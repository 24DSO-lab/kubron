// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * События бэкенда в браузере: один общий WebSocket /api/ws (internal/server/hub.go).
 *
 * - Подписки учитываются по именам событий с подсчётом обработчиков; на сервер уходит sub/unsub
 *   только при первом/последнем обработчике.
 * - При разрыве соединение восстанавливается с нарастающей паузой, подписки отправляются заново,
 *   а обработчикам псевдособытия RECONNECTED сообщается, что часть событий могла пропасть.
 * - Код закрытия 1012 «перезапуск сервиса» — реплика остановлена штатно (ADR-0016): другая реплика
 *   уже готова, поэтому переподключение быстрое (с разбросом) и без нарастания паузы.
 * - flushSubscriptions() — ожидание подтверждения подписок. Вызывается перед каждым RPC:
 *   запрос, запускающий поток (терминал, логи), не должен обогнать подписку на его события.
 */

export type Unsubscribe = () => void
type Handler = (...args: any[]) => void

/** Псевдособытие: соединение восстановлено после разрыва, состояние стоит перечитать. */
export const RECONNECTED = 'platform:reconnected'

const FLUSH_TIMEOUT_MS = 2000
const MAX_BACKOFF_MS = 30000
/** Код закрытия WebSocket «Service Restart» (RFC 6455): реплика останавливается штатно. */
export const CLOSE_SERVICE_RESTART = 1012
const RESTART_RECONNECT_MS = 1000 // верхняя граница случайной паузы после 1012

const handlers = new Map<string, Set<Handler>>()
let ws: WebSocket | null = null
let ready = false // получен кадр ready: сервер готов принимать подписки
let enabled = true // false после выхода/потери сессии, пока не будет нового входа
let attempt = 0
let hadConnection = false
let timer: ReturnType<typeof setTimeout> | undefined
let unacked = 0
let waiters: Array<() => void> = []

function wsUrl(): string {
  return `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/ws`
}

function send(frame: object): boolean {
  if (!ws || !ready || ws.readyState !== 1 /* OPEN */) return false
  ws.send(JSON.stringify(frame))
  return true
}

function settle() {
  if (unacked > 0) return
  const w = waiters
  waiters = []
  w.forEach(fn => fn())
}

function connect() {
  if (!enabled || ws || handlers.size === 0) return
  clearTimeout(timer)
  const sock = new WebSocket(wsUrl())
  ws = sock
  sock.onmessage = ev => {
    let msg: any
    try { msg = JSON.parse(String(ev.data)) } catch { return }
    if (msg.op === 'ready') {
      ready = true
      attempt = 0
      const names = [...handlers.keys()]
      if (names.length && send({ op: 'sub', events: names })) unacked++
      if (hadConnection) dispatch(RECONNECTED, [])
      hadConnection = true
    } else if (msg.op === 'sub_ok') {
      unacked = Math.max(0, unacked - 1)
      settle()
    } else if (typeof msg.event === 'string') {
      dispatch(msg.event, Array.isArray(msg.data) ? msg.data : [])
    }
  }
  sock.onclose = ev => {
    if (ws !== sock) return
    ws = null
    ready = false
    unacked = 0
    settle()
    scheduleReconnect(ev?.code === CLOSE_SERVICE_RESTART)
  }
  sock.onerror = () => { /* подробностей браузер не даёт; onclose последует следом */ }
}

function scheduleReconnect(restart = false) {
  if (!enabled || handlers.size === 0) return
  clearTimeout(timer)
  if (restart) {
    // Реплика ушла штатно: не наращиваем паузу, но разбрасываем клиентов, чтобы они не пришли разом.
    attempt = 0
    timer = setTimeout(connect, 100 + Math.random() * RESTART_RECONNECT_MS)
    return
  }
  const base = Math.min(MAX_BACKOFF_MS, 500 * 2 ** attempt++)
  timer = setTimeout(connect, base * (0.5 + Math.random() / 2))
}

function dispatch(event: string, args: any[]) {
  handlers.get(event)?.forEach(h => {
    try { h(...args) } catch (e) { console.error('event handler', event, e) }
  })
}

/** Подписка на событие бэкенда (cluster:changed, logs:data:<id>, terminal:data:<id> …). */
export function onEvent(name: string, handler: Handler): Unsubscribe {
  let set = handlers.get(name)
  if (!set) {
    set = new Set()
    handlers.set(name, set)
  }
  const first = set.size === 0
  set.add(handler)
  if (first && send({ op: 'sub', events: [name] })) unacked++
  connect()
  return () => {
    set!.delete(handler)
    if (set!.size === 0 && handlers.get(name) === set) {
      handlers.delete(name)
      send({ op: 'unsub', events: [name] })
    }
  }
}

/** Разрешает соединение (после входа) и открывает его, если есть подписчики. */
export function connectEvents() {
  enabled = true
  attempt = 0
  connect()
}

/** Закрывает соединение (выход, потеря сессии) и не переподключается до connectEvents(). */
export function closeEvents() {
  enabled = false
  clearTimeout(timer)
  const sock = ws
  ws = null
  ready = false
  unacked = 0
  settle()
  sock?.close()
}

/**
 * Ждёт подтверждения отправленных подписок (и открытия соединения, если оно устанавливается),
 * но не дольше FLUSH_TIMEOUT_MS: недоступность WebSocket не должна блокировать обычные вызовы.
 */
export function flushSubscriptions(): Promise<void> {
  if (!enabled || handlers.size === 0) return Promise.resolve()
  if (ready && unacked === 0) return Promise.resolve()
  return new Promise(resolve => {
    const done = () => { clearTimeout(t); resolve() }
    const t = setTimeout(() => { waiters = waiters.filter(w => w !== check); resolve() }, FLUSH_TIMEOUT_MS)
    const check = () => { if (ready && unacked === 0) done(); else waiters.push(check) }
    waiters.push(check)
  })
}

/** Только для тестов: сброс состояния модуля. */
export function __resetForTests() {
  closeEvents()
  handlers.clear()
  enabled = true
  attempt = 0
  hadConnection = false
  waiters = []
}
