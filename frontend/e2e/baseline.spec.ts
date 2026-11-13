// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, test } from '@playwright/test'

// Методы baseline (ADR-0027) закрыты аутентификацией и CSRF, как и остальные методы RPC.
// Сценарии с кластером (создание версии, проверка, исключения) требуют живого kube-apiserver и здесь не запускаются:
// они покрыты тестами внутренних пакетов на двойниках и общим контрактом хранилища.
test.describe('baseline: доступ к RPC', () => {
  for (const method of ['BaselineList', 'BaselineKinds', 'DriftCheck', 'BaselineDelete']) {
    test(`${method} без сессии отклоняется`, async ({ request }) => {
      const res = await request.post(`/api/rpc/${method}`, { data: [], headers: { 'Content-Type': 'application/json' } })
      expect([401, 403]).toContain(res.status())
    })
  }
})
