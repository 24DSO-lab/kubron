// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { handleOwnerSession, type OwnerAuthEnv } from '../../../relay/donationalerts-auth.ts'

interface Context { request: Request; env: OwnerAuthEnv }
export const onRequest = ({ request, env }: Context): Promise<Response> => handleOwnerSession(request, env)
