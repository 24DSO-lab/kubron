// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Права. Значения должны совпадать с internal/auth/permissions.go.
 * Интерфейс проверяет именно права (can), а не роли: новая роль на бэкенде
 * не требует правок на фронтенде.
 */
export const Perm = {
  Read: 'read',
  SecretsRead: 'secrets.read',
  Operate: 'operate',
  Write: 'write',
  Exec: 'exec',
  NodeShell: 'nodeshell',
  PortForward: 'portforward',
  ClusterManage: 'clusters.manage',
  UsersManage: 'users.manage',
  AuditRead: 'audit.read',
} as const

export type Permission = (typeof Perm)[keyof typeof Perm]

/** Пользователь из Go (internal/auth.UserInfo) */
export interface UserInfo {
  id: string
  username: string
  role: string
  roleLabel: string
  permissions: string[]
  disabled: boolean
  mustChangePassword: boolean
  createdAt: string
  lastLoginAt: string
}

/** Роль из Go (internal/auth.RoleInfo) */
export interface RoleInfo {
  id: string
  label: string
  permissions: string[]
}

/** Подписи прав для интерфейса управления пользователями: ключи auth.perm.*, текст рисует tl() */
export const PERMISSION_LABELS: Record<string, string> = {
  [Perm.Read]: 'auth.perm.read',
  [Perm.SecretsRead]: 'auth.perm.secretsRead',
  [Perm.Operate]: 'auth.perm.operate',
  [Perm.Write]: 'auth.perm.write',
  [Perm.Exec]: 'auth.perm.exec',
  [Perm.NodeShell]: 'auth.perm.nodeshell',
  [Perm.PortForward]: 'auth.perm.portforward',
  [Perm.ClusterManage]: 'auth.perm.clustersManage',
  [Perm.UsersManage]: 'auth.perm.usersManage',
  [Perm.AuditRead]: 'auth.perm.auditRead',
}
