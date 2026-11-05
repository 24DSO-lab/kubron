// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Провод RPC входа в кластер (зеркало internal/credential/contract.go, версия 1, JSON camelCase).
 * Секреты (пароль, токен, client-secret) в этих типах не хранятся: ответ пользователя живёт только
 * в локальном состоянии формы и уходит в AnswerChallenge.
 */

export type CredentialScope = 'user' | 'team'

export type CredentialKind = 'static' | 'oidc' | 'cloud-eks' | 'cloud-gke' | 'cloud-aks' | 'exec'

export type ChallengeKind =
  | 'credentials'
  | 'device-code'
  | 'browser-loopback'
  | 'browser-callback'
  | 'paste-redirect'
  | 'secret-fields'
  | 'manual-token'
  | 'confirm-exec'

export interface ChallengeField {
  /** Подпись: ключ i18n credential.field.<name> */
  name: string
  secret: boolean
}

/** Блок exec из kubeconfig; значения Env обнулены бэкендом, видны только имена. */
export interface ChallengeExec {
  apiVersion: string
  command: string
  args: string[] | null
  env?: Record<string, string>
  interactiveMode?: string
  installHint?: string
}

export interface Challenge {
  id: string
  kind: ChallengeKind
  fields?: ChallengeField[]
  url?: string
  userCode?: string
  /** manual-token: команда только для показа, интерфейс её никогда не выполняет */
  command?: string[]
  exec?: ChallengeExec
  /** Вывод плагина, только для показа (текст, не разметка) */
  message?: string
  poll?: boolean
  intervalSec?: number
  expiresAt: string
}

export interface ConnectResult {
  status: 'connected' | 'challenge'
  challenge?: Challenge
}

export interface ConnectionInfo {
  clusterId: string
  kind: CredentialKind
  scope: CredentialScope
  principal?: string
  expiresAt?: string
}

/** Ответ пользователя: имя поля → значение. Значения секретные. */
export type ChallengeValues = Record<string, string>

/** Решение для confirm-exec */
export type ExecDecision = 'once' | 'always'

/** Событие cluster:auth-completed */
export interface AuthCompletedEvent {
  clusterId: string
  challengeId: string
}

/** Событие cluster:auth-required */
export interface AuthRequiredEvent {
  clusterId: string
}

/** Настройка OAuth-клиента Google на десктопе (app.GKEOAuthStatus). Секрета клиента здесь нет и быть не может */
export interface GKEOAuthStatus {
  configured: boolean
  clientId: string
  secretSet: boolean
  restartRequired: boolean
}
