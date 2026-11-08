// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Контракты Flux CD: зеркало Go-структур internal/k8s/flux_types.go (имена полей = JSON).
 * Наружу не отдаются: spec.values, valuesFrom, postBuild.substitute, секреты kubeConfig и decryption,
 * userinfo в URL, last-applied-configuration, путь адреса Provider, webhookPath Receiver (ADR-0023, раздел 4).
 */

export type FluxState = 'Suspended' | 'Stalled' | 'Failed' | 'Reconciling' | 'Ready' | 'Unknown'

export type FluxStatusState = 'ok' | 'unsupportedVersion' | ''

export interface FluxControllers {
  kustomize: boolean
  helm: boolean
  source: boolean
  notification: boolean
  image: boolean
}

export interface FluxStatus {
  /** раздел Flux показывается: есть контроллер kustomize или helm */
  installed: boolean
  state: FluxStatusState
  controllers: FluxControllers
  /** обслуживаемая версия API по виду: «Kustomization» → «v1» */
  versions: Record<string, string>
}

/** Ссылка на объект Flux; у dependsOn вид не указывается (kind пуст) */
export interface FluxRef {
  kind: string
  namespace: string
  name: string
}

export interface FluxActionResult {
  kind: string
  namespace: string
  name: string
  /** пусто — успех */
  error: string
}

export interface FluxCondition {
  type: string
  status: string
  lastTransitionTime: string
  reason: string
  message: string
}

/** Общая часть всех объектов Flux (Go: FluxObjectInfo встраивается плоско) */
export interface FluxObjectInfo {
  kind: string
  name: string
  namespace: string
  age: string
  interval: string
  suspended: boolean
  state: FluxState
  sourceRef: FluxRef | null
  dependsOn: FluxRef[]
  conditions: FluxCondition[]
  /** status.lastAppliedRevision; у источников — ревизия артефакта */
  revision: string
  lastAttemptedRevision: string
  lastReconcileAt: string
  message: string
  labels: Record<string, string>
  annotations: Record<string, string>
  created: string
}

export interface FluxKustomizationInfo extends FluxObjectInfo {
  path: string
  prune: boolean
  wait: boolean
  targetNamespace: string
  serviceAccount: string
  /** задан spec.kubeConfig: ресурсы inventory в другом кластере */
  remote: boolean
  inventoryCount: number
}

export interface FluxHelmHistoryEntry {
  version: number
  status: string
  chartName: string
  chartVersion: string
  appVersion: string
  firstDeployed: string
  lastDeployed: string
  digest: string
}

export interface FluxHelmReleaseInfo extends FluxObjectInfo {
  chart: string
  chartVersion: string
  /** spec.chartRef; null, если чарт задан через spec.chart.spec */
  chartRef: FluxRef | null
  releaseName: string
  targetNamespace: string
  storageNamespace: string
  remote: boolean
  /** есть status.inventory (Flux 2.8+); без него вкладка ресурсов недоступна */
  hasInventory: boolean
  inventoryCount: number
  /** новые записи первыми */
  history: FluxHelmHistoryEntry[]
}

/** GitRepository, OCIRepository, HelmRepository, HelmChart или Bucket (вид — в kind) */
export interface FluxSourceInfo extends FluxObjectInfo {
  /** без userinfo */
  url: string
  /** branch | tag | semver | name | commit | digest; пусто — не задано */
  refType: string
  refValue: string
  digest: string
  /** только имя секрета */
  secretRef: string
  chart: string
  chartVersion: string
  bucketName: string
}

export interface FluxProviderInfo extends FluxObjectInfo {
  type: string
  channel: string
  /** только схема и хост */
  address: string
  secretRef: string
}

export interface FluxAlertInfo extends FluxObjectInfo {
  providerRef: string
  eventSeverity: string
  eventSources: FluxRef[]
}

export interface FluxReceiverInfo extends FluxObjectInfo {
  type: string
  resources: FluxRef[]
  secretRef: string
}

export interface FluxNotifications {
  providers: FluxProviderInfo[]
  alerts: FluxAlertInfo[]
  receivers: FluxReceiverInfo[]
}

export interface FluxImageRepositoryInfo extends FluxObjectInfo {
  image: string
  tagCount: number
  lastScanAt: string
}

export interface FluxImagePolicyInfo extends FluxObjectInfo {
  imageRepository: string
  /** правило: «semver: >=1.0.0» и т. п. */
  policy: string
  latestImage: string
}

export interface FluxImageUpdateAutomationInfo extends FluxObjectInfo {
  branch: string
  lastRunAt: string
  lastPushCommit: string
}

export interface FluxImageAutomation {
  repositories: FluxImageRepositoryInfo[]
  policies: FluxImagePolicyInfo[]
  automations: FluxImageUpdateAutomationInfo[]
}

export type FluxGraphRole = 'source' | 'flux' | 'resource'
export type FluxGraphEdgeType = 'owns' | 'selects' | 'routes' | 'sourceRef' | 'dependsOn' | 'inventory'

export interface FluxGraphNode {
  /** group/kind/namespace/name */
  id: string
  kind: string
  group: string
  version: string
  namespace: string
  name: string
  role: FluxGraphRole
  /** FluxState для source и flux; для ресурсов — health (Healthy, Degraded, Progressing) или пусто */
  state: string
}

export interface FluxGraphEdge {
  from: string
  to: string
  type: FluxGraphEdgeType
}

export interface FluxGraph {
  /** у HelmRelease на Flux < 2.8 нет status.inventory: граф недоступен */
  available: boolean
  /** ресурсы inventory в другом кластере, локально не разрешаются */
  remote: boolean
  nodes: FluxGraphNode[]
  edges: FluxGraphEdge[]
  truncated: boolean
  warnings: string[]
}
