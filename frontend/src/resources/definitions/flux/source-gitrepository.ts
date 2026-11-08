// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineFluxSource } from './sourceCommon'

/** GitRepository (Flux CD): колонки, действия и вкладки общие для источников — см. sourceCommon.ts */
export const fluxGitRepository = defineFluxSource({
  id: 'flux-gitrepository',
  kind: 'GitRepository',
  view: 'flux-gitrepositories',
  title: 'GitRepositories',
  plural: 'gitrepositories',
})
