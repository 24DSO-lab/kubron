// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineFluxSource } from './sourceCommon'

/** OCIRepository (Flux CD): колонки, действия и вкладки общие для источников — см. sourceCommon.ts */
export const fluxOCIRepository = defineFluxSource({
  id: 'flux-ocirepository',
  kind: 'OCIRepository',
  view: 'flux-ocirepositories',
  title: 'OCIRepositories',
  plural: 'ocirepositories',
})
