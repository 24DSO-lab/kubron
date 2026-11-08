// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineFluxSource } from './sourceCommon'

/** HelmChart (Flux CD): колонки, действия и вкладки общие для источников — см. sourceCommon.ts */
export const fluxHelmChart = defineFluxSource({
  id: 'flux-helmchart',
  kind: 'HelmChart',
  view: 'flux-helmcharts',
  title: 'HelmCharts',
  plural: 'helmcharts',
})
