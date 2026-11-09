// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Контракт app.BugReport* (internal/app/app_bugreport.go). Wails описывает эти структуры классами, поэтому типы свои. */

export type MailSecurity = 'starttls' | 'tls' | 'none'

export interface BugReportSettings {
  host: string
  port: number
  username: string
  /** только на запись: бэкенд пароль не возвращает */
  password: string
  from: string
  to: string
  security: MailSecurity
  passwordSet: boolean
}

export interface BugReportStatus {
  configured: boolean
  source: 'saved' | 'environment' | ''
  recipient: string
  canSave: boolean
  /** десктоп может открыть почтовую программу с готовым письмом */
  canDraft: boolean
  settings: BugReportSettings
}

export interface BugReportRequest {
  description: string
  contact: string
  /** PNG в base64 без префикса data: ; пусто — без снимка */
  screenshot: string
  locale: string
  view: string
}

/** Результат OpenBugReportDraft: что подготовлено рядом с письмом */
export interface BugReportDraft {
  screenshotPath: string
  textPath: string
  /** описание не влезло в ссылку и сокращено; полный текст в textPath */
  truncated: boolean
}
