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

/** Куда пойдёт следующий отчёт: почтовый сервер, приёмник отчётов или никуда (останется в очереди) */
export type BugReportChannel = 'mail' | 'relay' | ''

export interface BugReportStatus {
  /** задан почтовый сервер */
  configured: boolean
  source: 'saved' | 'environment' | ''
  recipient: string
  canSave: boolean
  channel: BugReportChannel
  /** отчёт, который не удалось доставить, сохраняется и уйдёт позже (десктоп) */
  canQueue: boolean
  /** сколько отчётов ждёт отправки */
  pending: number
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

/** Что стало с отчётом: ушёл или сохранён до появления связи */
export interface BugReportResult {
  status: 'sent' | 'queued'
  channel: BugReportChannel
  pending: number
}
