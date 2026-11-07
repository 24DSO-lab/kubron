// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Вход в кластер: RPC и события (контракт internal/credential, ADR-0018). Единственное место, где
 * фронтенд обращается к методам ConnectCluster / AnswerChallenge / PollChallenge / CancelChallenge /
 * ListMyConnections / DisconnectCluster. Имена методов и аргументы проверяет vue-tsc по сгенерированным
 * привязкам Wails (wailsjs/go/main/App.d.ts); результат приводится к типам из types/credential.ts, потому что
 * Wails описывает time.Time и строковые перечисления Go как any и string.
 */
import { callApp } from './api'
import { onEvent, type Unsubscribe } from './events'
import type {
  AuthCompletedEvent, AuthRequiredEvent, ChallengeValues, ConnectionInfo, ConnectResult, GKEOAuthStatus,
} from '../types/credential'

export const EVENT_AUTH_COMPLETED = 'cluster:auth-completed'
export const EVENT_AUTH_REQUIRED = 'cluster:auth-required'

export async function connectCluster(clusterId: string): Promise<ConnectResult> {
  return (await callApp('ConnectCluster', clusterId)) as unknown as ConnectResult
}

/** Ответ на вызов. Возвращает следующий шаг: новый вызов или connected. */
export async function answerChallenge(challengeId: string, values: ChallengeValues): Promise<ConnectResult> {
  return (await callApp('AnswerChallenge', challengeId, values)) as unknown as ConnectResult
}

export async function pollChallenge(challengeId: string): Promise<ConnectResult> {
  return (await callApp('PollChallenge', challengeId)) as unknown as ConnectResult
}

export async function cancelChallenge(challengeId: string): Promise<void> {
  await callApp('CancelChallenge', challengeId)
}

export async function listMyConnections(): Promise<ConnectionInfo[]> {
  return ((await callApp('ListMyConnections')) as unknown as ConnectionInfo[] | null) ?? []
}

export async function disconnectCluster(clusterId: string): Promise<void> {
  await callApp('DisconnectCluster', clusterId)
}

export function onAuthCompleted(handler: (e: AuthCompletedEvent) => void): Unsubscribe {
  return onEvent(EVENT_AUTH_COMPLETED, handler)
}

export function onAuthRequired(handler: (e: AuthRequiredEvent) => void): Unsubscribe {
  return onEvent(EVENT_AUTH_REQUIRED, handler)
}

/** Настройка OAuth-клиента Google для входа в GKE (только десктоп; в вебе клиент задаёт администратор сервера). */
export async function getGKEOAuth(): Promise<GKEOAuthStatus> {
  return (await callApp('GetGKEOAuth')) as unknown as GKEOAuthStatus
}

/** Пустой секрет оставляет сохранённый для того же идентификатора клиента. Значение уходит только сюда. */
export async function saveGKEOAuth(clientId: string, clientSecret: string): Promise<void> {
  await callApp('SaveGKEOAuth', clientId, clientSecret)
}

export async function clearGKEOAuth(): Promise<void> {
  await callApp('ClearGKEOAuth')
}
