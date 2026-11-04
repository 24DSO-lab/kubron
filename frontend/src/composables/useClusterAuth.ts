// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'
import * as api from '../platform/credential'
import { errorText } from './errorText'
import { isAnswerRejected, isChallengeExpired, isInvalidGrant } from '../i18n/backendErrors'
import { pollIntervalMs, secondsLeft } from '../components/credential/challengeView'
import type { Challenge, ChallengeValues, ConnectResult } from '../types/credential'
import type { Unsubscribe } from '../platform/events'

/** Пользователь закрыл диалог входа, не завершив его */
export class AuthCancelledError extends Error {
  constructor() {
    super('sign-in cancelled')
    this.name = 'AuthCancelledError'
  }
}

/** Что нужно composable от окружения: RPC, часы и таймеры подменяются в тестах. */
export interface ClusterAuthDeps {
  api: Pick<typeof api, 'connectCluster' | 'answerChallenge' | 'pollChallenge' | 'cancelChallenge' | 'onAuthCompleted' | 'onAuthRequired'>
  now: () => number
  setTimeout: (fn: () => void, ms: number) => unknown
  clearTimeout: (h: unknown) => void
  setInterval: (fn: () => void, ms: number) => unknown
  clearInterval: (h: unknown) => void
}

const MAX_POLL_FAILURES = 3

/**
 * Состояние диалога входа. В нём нет ответов пользователя: пароль и токен живут только в локальном
 * состоянии формы и уходят прямо в AnswerChallenge, ни в хранилище браузера, ни сюда не попадают.
 */
export function createClusterAuth(deps: ClusterAuthDeps) {
  const challenge = ref<Challenge | null>(null)
  const clusterId = ref('')
  const busy = ref(false)
  const error = ref('')
  /** Растёт после отклонённого ответа: форма очищает секретные поля */
  const attempt = ref(0)
  const nowMs = ref(deps.now())

  let waiter: { resolve: () => void; reject: (e: unknown) => void } | null = null
  let pollTimer: unknown = null
  let tickTimer: unknown = null
  let pollFailures = 0

  const remainingSec = computed(() => (challenge.value ? secondsLeft(challenge.value.expiresAt, nowMs.value) : 0))
  const expired = computed(() => challenge.value !== null && remainingSec.value === 0)

  function stopTimers() {
    if (pollTimer !== null) deps.clearTimeout(pollTimer)
    if (tickTimer !== null) deps.clearInterval(tickTimer)
    pollTimer = null
    tickTimer = null
  }

  function close() {
    stopTimers()
    challenge.value = null
    clusterId.value = ''
    busy.value = false
    error.value = ''
    pollFailures = 0
  }

  function finish() {
    const w = waiter
    waiter = null
    close()
    w?.resolve()
  }

  function abort(reason: unknown) {
    const w = waiter
    waiter = null
    close()
    w?.reject(reason)
  }

  function show(id: string, c: Challenge) {
    stopTimers()
    clusterId.value = id
    challenge.value = c
    error.value = ''
    pollFailures = 0
    nowMs.value = deps.now()
    tickTimer = deps.setInterval(() => { nowMs.value = deps.now() }, 1000)
    if (c.poll) schedulePoll(c.id, pollIntervalMs(c.intervalSec))
  }

  function schedulePoll(id: string, ms: number) {
    pollTimer = deps.setTimeout(() => { void pollOnce(id) }, ms)
  }

  function applyResult(id: string, res: ConnectResult) {
    if (res.status === 'connected') return finish()
    if (res.challenge) return show(id, res.challenge)
    // Бэкенд вернул challenge без тела: продолжать нечем
    error.value = errorText(new Error('the sign-in request has expired'))
  }

  async function pollOnce(challengeId: string) {
    const current = challenge.value
    if (!current || current.id !== challengeId) return
    try {
      const res = await deps.api.pollChallenge(challengeId)
      if (challenge.value?.id !== challengeId) return
      pollFailures = 0
      if (res.status === 'challenge' && res.challenge && res.challenge.id === challengeId) {
        // Ещё ждём: срок и интервал могли поменяться (slow_down)
        challenge.value = res.challenge
        schedulePoll(challengeId, pollIntervalMs(res.challenge.intervalSec))
        return
      }
      applyResult(clusterId.value, res)
    } catch (e) {
      if (challenge.value?.id !== challengeId) return
      const message = e instanceof Error ? e.message : String(e)
      if (isChallengeExpired(message) || isInvalidGrant(message)) {
        error.value = errorText(e)
        return stopTimersKeepDialog()
      }
      pollFailures++
      error.value = errorText(e)
      if (pollFailures >= MAX_POLL_FAILURES) return stopTimersKeepDialog()
      schedulePoll(challengeId, pollIntervalMs(current.intervalSec))
    }
  }

  /** Опрос остановлен ошибкой: диалог остаётся с текстом ошибки, закрыть можно кнопкой */
  function stopTimersKeepDialog() {
    if (pollTimer !== null) deps.clearTimeout(pollTimer)
    pollTimer = null
  }

  /**
   * Подключиться к кластеру. Если нужен вход, показывает диалог и ждёт: resolve — подключено,
   * reject AuthCancelledError — пользователь отменил, иной reject — ошибка вызова.
   */
  async function connect(id: string): Promise<void> {
    if (waiter) abort(new AuthCancelledError())
    const res = await deps.api.connectCluster(id)
    if (res.status === 'connected') return
    if (!res.challenge) throw new Error('the sign-in request has expired')
    return new Promise<void>((resolve, reject) => {
      waiter = { resolve, reject }
      show(id, res.challenge as Challenge)
    })
  }

  /** Отправить ответ. Значения уходят только в RPC и нигде не сохраняются. */
  async function submit(values: ChallengeValues): Promise<void> {
    const c = challenge.value
    if (!c || busy.value) return
    busy.value = true
    error.value = ''
    try {
      applyResult(clusterId.value, await deps.api.answerChallenge(c.id, values))
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e)
      error.value = errorText(e)
      if (isAnswerRejected(message)) attempt.value++ // повтор на том же вызове
      else if (isChallengeExpired(message) || isInvalidGrant(message)) stopTimersKeepDialog()
    } finally {
      if (challenge.value) busy.value = false
    }
  }

  /** Отмена пользователем: бэкенд уведомляется, ошибка отмены не мешает закрыть диалог. */
  async function cancel(): Promise<void> {
    const c = challenge.value
    if (!c) return
    abort(new AuthCancelledError())
    try {
      await deps.api.cancelChallenge(c.id)
    } catch {
      // вызов мог уже истечь на сервере
    }
  }

  /** Диалог показан после ошибки/истечения: начать вход заново */
  async function restart(): Promise<void> {
    const id = clusterId.value
    const w = waiter
    waiter = null
    const old = challenge.value
    close()
    if (old) void deps.api.cancelChallenge(old.id).catch(() => {})
    try {
      await connectInto(id, w)
    } catch (e) {
      w?.reject(e)
    }
  }

  async function connectInto(id: string, w: typeof waiter) {
    const res = await deps.api.connectCluster(id)
    if (res.status === 'connected') return w?.resolve()
    if (!res.challenge) throw new Error('the sign-in request has expired')
    waiter = w
    show(id, res.challenge)
  }

  /**
   * Подписка на события бэкенда. cluster:auth-completed закрывает диалог этого кластера (вход завершён
   * в другом окне или через loopback); cluster:auth-required просит войти заново.
   */
  function listen(onRequired?: (clusterId: string) => void): Unsubscribe {
    const offDone = deps.api.onAuthCompleted((e) => {
      if (challenge.value && e?.clusterId === clusterId.value) finish()
    })
    const offReq = deps.api.onAuthRequired((e) => {
      if (e?.clusterId) onRequired?.(e.clusterId)
    })
    return () => { offDone(); offReq() }
  }

  return { challenge, clusterId, busy, error, attempt, remainingSec, expired, connect, submit, cancel, restart, listen }
}

let shared: ReturnType<typeof createClusterAuth> | null = null

/** Общее состояние входа для приложения (один диалог на окно). */
export function useClusterAuth() {
  shared ??= createClusterAuth({
    api,
    now: () => Date.now(),
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    setInterval: (fn, ms) => setInterval(fn, ms),
    clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
  })
  return shared
}
