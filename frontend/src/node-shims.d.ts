// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Минимальные типы Node для тестов, которые читают золотые файлы бэкенда (incidents.golden.test.ts). Пакета
// @types/node в проекте нет, а `vue-tsc --noEmit` проверяет и тесты. Когда @types/node появится, этот файл можно удалить.
declare module 'node:fs' {
  export interface Dirent {
    name: string
    isDirectory(): boolean
  }
  export function existsSync(path: string): boolean
  export function readFileSync(path: string, encoding: 'utf8'): string
  export function readdirSync(path: string): string[]
  export function readdirSync(path: string, options: { withFileTypes: true }): Dirent[]
}

declare module 'node:url' {
  export function fileURLToPath(url: string | URL): string
}
