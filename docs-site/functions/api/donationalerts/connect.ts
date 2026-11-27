// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { handleConnect, type DonationAlertsEnv } from '../../../relay/donationalerts.ts'

interface Context { request: Request; env: DonationAlertsEnv }
export const onRequest = ({ request, env }: Context): Promise<Response> => handleConnect(request, env)
