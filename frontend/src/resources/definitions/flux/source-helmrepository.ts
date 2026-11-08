// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineFluxSource } from './sourceCommon'

/** HelmRepository (Flux CD): колонки, действия и вкладки общие для источников — см. sourceCommon.ts */
export const fluxHelmRepository = defineFluxSource({
  id: 'flux-helmrepository',
  kind: 'HelmRepository',
  view: 'flux-helmrepositories',
  title: 'HelmRepositories',
  plural: 'helmrepositories',
})
