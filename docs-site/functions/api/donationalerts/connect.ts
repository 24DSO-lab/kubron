// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { handleConnect, type DonationAlertsEnv } from '../../../relay/donationalerts.ts'
import { handleOwnerConnect, type OwnerAuthEnv } from '../../../relay/donationalerts-auth.ts'

interface Context { request: Request; env: DonationAlertsEnv & OwnerAuthEnv }
export const onRequest = ({ request, env }: Context): Promise<Response> =>
  handleOwnerConnect(request, env, () => handleConnect(request, env))
