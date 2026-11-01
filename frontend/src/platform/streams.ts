// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { uid } from '../resources/uid'
import { waitReplica } from './events.web'

/** Код ошибки сервера: поток открыт на другой реплике (internal/streamid, ADR-0016). */
export const STREAM_ELSEWHERE = 'stream_elsewhere'

/**
 * Идентификатор потока (логи). Веб: «<реплика>.<kind>_<uuid>», где реплика — та, с которой браузер держит
 * сокет событий, чтобы события потока пришли в этот же сокет. Десктоп и одна реплика без префикса.
 * Если сокет событий ещё не открыт, ждёт его (не дольше пары секунд).
 */
export async function newStreamId(kind: string): Promise<string> {
  const base = `${kind}_${uid()}`
  if (import.meta.env.VITE_TARGET !== 'web') return base
  const r = await waitReplica()
  return r ? `${r}.${base}` : base
}

const isElsewhere = (e: unknown) => (e as { code?: string } | null)?.code === STREAM_ELSEWHERE

/**
 * Повторяет запрос, пока балансировщик не направит его на реплику-владельца потока (stream_elsewhere).
 * Без sticky-affinity по cookie это штатная ситуация; остальные ошибки не повторяются.
 */
export async function retryElsewhere<T>(fn: () => Promise<T>, tries = 8, pause = () => 100 + Math.random() * 200): Promise<T> {
  for (let i = 1; ; i++) {
    try {
      return await fn()
    } catch (e) {
      if (!isElsewhere(e) || i >= tries) throw e
      await new Promise(r => setTimeout(r, pause()))
    }
  }
}
