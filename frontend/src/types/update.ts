// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Проверка обновлений (ADR-0028). Формы совпадают с Go: internal/update.Version и internal/update.Info.
 * Строковые поля оставлены строками: набор способов установки и кодов ошибок расширяется на бэкенде,
 * а интерфейс для неизвестного значения показывает запасной вариант, а не падает.
 */

/** Версия этой сборки; видна любому вошедшему пользователю */
export interface VersionInfo {
  version: string
  /** desktop | web */
  target: string
}

export interface UpdateInstall {
  target: string
  /** helm | kubernetes | docker | binary | deb | archive | macos-app | unknown */
  method: string
  os: string
  arch: string
  helmRelease?: string
  helmNamespace?: string
}

export interface UpdateCommand {
  /** Выбирает подпись: update.command.<id> */
  id: string
  text: string
}

export interface UpdateDownload {
  name: string
  url: string
  size?: number
  checksumsUrl?: string
}

/** Состояние проверки; подробности установки и команды получает только администратор */
export interface UpdateInfo {
  current: string
  install: UpdateInstall
  locked: boolean
  autoCheck: boolean
  latest?: string
  available: boolean
  releaseUrl?: string
  publishedAt?: string
  checkedAt?: string
  /** Код последней неудачной проверки: update.error.<код> */
  error?: string
  download?: UpdateDownload
  commands?: UpdateCommand[]
}
