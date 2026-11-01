// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from '@playwright/test'

/**
 * e2e высокой доступности (ADR-0016, задача HA): две реплики kubron-server на одной PostgreSQL с ПУСТОЙ базой.
 * Реплика A — E2E_HA_A_URL (по умолчанию http://127.0.0.1:8081), B — E2E_HA_B_URL (http://127.0.0.1:8082).
 * Тест гибели реплики завершает процесс A сигналом: его PID в E2E_HA_A_PID. Подготовка — docs/e2e.md.
 */
export default defineConfig({
  testDir: './e2e-ha',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0, // тесты идут по порядку и меняют состояние (в конце реплика A останавливается)
  reporter: process.env.CI ? [['list'], ['html', { open: 'never', outputFolder: 'playwright-report-ha' }]] : 'list',
  use: {
    baseURL: process.env.E2E_HA_B_URL ?? 'http://127.0.0.1:8082',
    locale: 'ru-RU',
    launchOptions: process.env.E2E_CHROMIUM ? { executablePath: process.env.E2E_CHROMIUM } : {},
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
})
