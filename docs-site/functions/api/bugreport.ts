// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// POST /api/bugreport — приём отчётов из приложения. Вся логика в ../../relay/bugreport.ts (она проверяется тестами).
// onRequest ловит все методы: всё, что не POST, получает 405, а не статическую страницу.
import { handleReport, type Env } from '../../relay/bugreport.ts'

interface Context {
  request: Request
  env: Env
}

export const onRequest = ({ request, env }: Context): Promise<Response> => handleReport(request, env)
