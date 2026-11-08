// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineFluxSource } from './sourceCommon'

/** Bucket (Flux CD): колонки, действия и вкладки общие для источников — см. sourceCommon.ts */
export const fluxBucket = defineFluxSource({
  id: 'flux-bucket',
  kind: 'Bucket',
  view: 'flux-buckets',
  title: 'Buckets',
  plural: 'buckets',
})
