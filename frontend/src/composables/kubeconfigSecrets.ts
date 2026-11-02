// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/** Значение скрытого секрета в тексте kubeconfig (зеркало k8s.MaskPlaceholder): сервер подставит прежнее значение сам */
export const SECRET_PLACEHOLDER = '<kubron:unchanged>'

export function hasHiddenSecret(text: string): boolean {
  return text.includes(SECRET_PLACEHOLDER)
}

/** Границы первого плейсхолдера — чтобы выделить его, и вводимое значение заменило секрет; null — плейсхолдера нет */
export function firstPlaceholderRange(text: string): [number, number] | null {
  const at = text.indexOf(SECRET_PLACEHOLDER)
  return at < 0 ? null : [at, at + SECRET_PLACEHOLDER.length]
}
