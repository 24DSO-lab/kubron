// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { handleDonationAlertsSync } from '../../../relay/donationalerts-sync.ts'
import type { DonationAlertsEnv } from '../../../relay/donationalerts.ts'

interface Context {
  request: Request
  env: DonationAlertsEnv & { DONATIONALERTS_SYNC_SECRET?: string }
}

export const onRequest = ({ request, env }: Context): Promise<Response> =>
  handleDonationAlertsSync(request, env)
