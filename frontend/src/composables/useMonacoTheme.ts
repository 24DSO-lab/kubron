// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { watch } from 'vue'
import { monacoThemeName } from './themePalettes'
import { useTheme } from './useTheme'

/**
 * Привязывает тему Monaco к теме приложения. apply — monaco.editor.setTheme (передаётся, чтобы composable
 * не тянул Monaco в чанк, где редактора нет): тема в Monaco общая для всех редакторов, поэтому вызов
 * из нескольких компонентов безвреден. Возвращает функцию, дающую имя темы для создаваемого редактора.
 * Подписка снимается вместе с компонентом.
 */
export function useMonacoTheme(apply: (name: string) => void): () => string {
  const { resolved } = useTheme()
  watch(resolved, r => apply(monacoThemeName(r)))
  return () => monacoThemeName(resolved.value)
}
