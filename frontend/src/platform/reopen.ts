// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Политика автоматического переоткрытия терминала после потери соединения (реплика остановилась, ADR-0016).
 * Число подряд идущих попыток ограничено, чтобы не зациклиться, если терминал не открывается; после
 * healthyMs стабильной работы счётчик сбрасывается.
 */
export class ReopenPolicy {
  private attempts = 0
  private openedAt = 0

  constructor(private readonly max = 3, private readonly healthyMs = 30_000, private readonly now: () => number = Date.now) {}

  /** Терминал открылся (или переоткрылся) успешно. */
  opened() { this.openedAt = this.now() }

  /** Можно ли переоткрыть терминал сейчас; true засчитывает попытку. */
  allow(): boolean {
    if (this.now() - this.openedAt > this.healthyMs) this.attempts = 0
    if (this.attempts >= this.max) return false
    this.attempts++
    return true
  }
}
