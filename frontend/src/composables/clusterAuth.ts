// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { ClusterMeta } from '../types'

/** Значение ClusterMeta.mode общей учётной записи команды (k8s.ModeTeamAccount; строка осталась прежней в хранилище) */
export const TEAM_ACCOUNT_MODE = 'oidc-password'

/**
 * Получает ли контекст учётные данные входом (oidc, облачный плагин, другой exec), а не статически.
 * auth — credential.Kind из InspectKubeconfig: static, oidc, cloud-eks, cloud-gke, cloud-aks, exec.
 * У старых ответов поля нет: считаем, что входа нет.
 */
export function signsIn(auth: string | undefined): boolean {
  return !!auth && auth !== 'static'
}

/**
 * Можно ли предложить «общую учётную запись команды»: кластер принадлежит команде и входит через провайдера.
 * Вход под общей записью вводит администратор кластера; вызывающий уже выбрал команду, где он администратор.
 */
export function canShareAccount(auth: string | undefined, hasTeam: boolean): boolean {
  return hasTeam && signsIn(auth)
}

/** Кластер входит под общей учётной записью команды */
export function isSharedAccount(cluster: Pick<ClusterMeta, 'mode'>): boolean {
  return cluster.mode === TEAM_ACCOUNT_MODE
}
