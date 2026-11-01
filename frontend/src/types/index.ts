// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

export interface ClusterMeta {
  id: string
  displayName: string
  /** Ключ значка из набора интерфейса; '' — монограмма (у старых кластеров может быть эмодзи) */
  icon: string
  color: string
  kubeconfigFile: string
  defaultNamespace: string
  addedAt: string
  contextName: string
  /** dev | test | stage | prod; '' — не задано */
  environment: string
  /** Команда-владелец (только сервисный режим) */
  teamId?: string
  /** Способ подключения: kubeconfig (или пусто), in-cluster, oidc-password (сервисный режим) */
  mode?: string
}

/** Результат фоновой проверки соединения с кластером (сервисный режим; в десктопе поля нет) */
export interface ClusterHealth {
  state: 'ok' | 'unreachable' | 'unauthorized' | 'misconfigured' | 'unknown'
  /** Причина как её вернул клиент Kubernetes (по-английски, без секретов) */
  message?: string
  latencyMs?: number
  checkedAt: string
}

export interface ClusterStatus extends ClusterMeta {
  connected: boolean
  active: boolean
  error: string
  health?: ClusterHealth
}

/** Свойства кластера, которые пользователь задаёт при добавлении и правке (зеркало k8s.ClusterSettings) */
export interface ClusterSettings {
  displayName: string
  icon: string
  color: string
  environment: string
  defaultNamespace: string
  contextName: string
}

/** Источник kubeconfig: задаётся ровно одно поле (зеркало k8s.KubeconfigSource) */
export interface KubeconfigSource {
  path: string
  text: string
  clusterId: string
}

export interface KubeconfigContextInfo {
  name: string
  cluster: string
  user: string
  namespace: string
  server: string
  /** Как контекст получает учётные данные (k8s.AuthKind); у старых ответов поля нет */
  auth?: KubeconfigAuth | string
}

/** static — токен, сертификат; oidc-password — oidc-login с паролем; oidc-authcode — oidc-login через браузер; exec — другой плагин */
export type KubeconfigAuth = 'static' | 'oidc-password' | 'oidc-authcode' | 'exec'

/** Логин и пароль для oidc-login: сервер сам получает токен у провайдера входа */
export interface OIDCCredentials {
  username: string
  password: string
}

export interface KubeconfigInfo {
  contexts: KubeconfigContextInfo[]
  currentContext: string
}

/** Результат пробного подключения; недоступный кластер — это ok=false, а не исключение */
export interface ConnectionCheck {
  ok: boolean
  serverVersion: string
  latencyMs: number
  warning: string
  error: string
}

export interface ClusterUpdateResult {
  cluster: ClusterMeta
  /** Изменился kubeconfig или контекст: прежнее подключение устарело */
  connectionChanged: boolean
}

/** Что отправляет диалог добавления кластера */
export type AddClusterPayload =
  | { kind: 'file'; path: string; settings: ClusterSettings }
  | {
    kind: 'text'; text: string; settings: ClusterSettings
    /** команда-владелец (сервисный режим) */
    teamId?: string
    /** для kubeconfig с oidc-login (grant password) */
    oidc?: OIDCCredentials
  }
  /** Кластер, в котором работает сам сервер (админ организации) */
  | { kind: 'in-cluster'; settings: ClusterSettings; teamId: string }

/** Итог операции, которая может завершиться понятной пользователю ошибкой */
export type Outcome<T> = { ok: true; value: T } | { ok: false; error: string }

export interface ContextInfo {
  name: string
  cluster: string
  user: string
  namespace: string
  isCurrent: boolean
}

export interface NamespaceInfo {
  name: string
  status: string
  age: string
}

export interface ConditionInfo {
  type: string
  status: string
  lastTransitionTime: string
  reason: string
  message: string
}

export interface TolerationInfo {
  key: string
  operator: string
  value: string
  effect: string
  tolerationSeconds?: number
}

export interface VolumeInfo {
  name: string
  type: string
  details: Record<string, string>
}

export interface PreferredDuringSchedulingIgnoredDuringExecution {
  weight: number
  podAffinityTerm: {
    labelSelector?: {
      matchExpressions?: Array<{
        key: string
        operator: string
        values?: string[]
      }>
      matchLabels?: Record<string, string>
    }
    namespaces?: string[]
    topologyKey: string
  }
}

export interface RequiredDuringSchedulingIgnoredDuringExecution {
  labelSelector?: {
    matchExpressions?: Array<{
      key: string
      operator: string
      values?: string[]
    }>
    matchLabels?: Record<string, string>
  }
  namespaces?: string[]
  topologyKey?: string
}

export interface PodAntiAffinity {
  preferredDuringSchedulingIgnoredDuringExecution?: PreferredDuringSchedulingIgnoredDuringExecution[]
  requiredDuringSchedulingIgnoredDuringExecution?: RequiredDuringSchedulingIgnoredDuringExecution[]
}

export interface PodInfo {
  name: string
  namespace: string
  status: string
  ready: string
  restarts: number
  age: string
  node: string
  ip: string
  labels: Record<string, string>
  annotations?: Record<string, string>
  controlledBy: string
  serviceAccount: string
  nodeSelector?: Record<string, string>
  conditions: ConditionInfo[]
  tolerations: TolerationInfo[]
  podAntiAffinity?: PodAntiAffinity
  containers: ContainerInfo[]
  volumes: VolumeInfo[]
  qos: string
  created: string
}

export interface ContainerInfo {
  name: string
  image: string
  ready: boolean
  restartCount: number
  state: string
  isInit: boolean
}

export interface PodDetailInfo {
  name: string
  namespace: string
  status: string
  ready: string
  restarts: number
  age: string
  node: string
  ip: string
  labels: Record<string, string>
  annotations?: Record<string, string>
  controlledBy: string
  serviceAccount: string
  nodeSelector?: Record<string, string>
  conditions: ConditionInfo[]
  tolerations: TolerationInfo[]
  podAntiAffinity?: PodAntiAffinity
  containers: ContainerInfo[]
  volumes: VolumeInfo[]
  qos: string
  created: string
}

export interface DeploymentInfo {
  name: string
  namespace: string
  replicas: number
  readyReplicas: number
  updatedReplicas: number
  age: string
  created?: string
  strategy: string
  images: string[]
  conditions?: ConditionInfo[]
  status?: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  selector?: string
  nodeSelector?: string
  podAntiAffinity?: PodAntiAffinityInfo
  revisions?: RevisionInfo[]
}

export interface RevisionInfo {
  revision: string
  name: string
  replicas: number
  ready: number
  age: string
  images: string
}

export interface PodAntiAffinityInfo {
  preferredDuringSchedulingIgnoredDuringExecution?: PreferredAffinityTermInfo[]
  requiredDuringSchedulingIgnoredDuringExecution?: RequiredAffinityTermInfo[]
}

export interface PreferredAffinityTermInfo {
  weight?: number
  podAffinityTerm?: PodAffinityTermInfo
}

export interface RequiredAffinityTermInfo {
  podAffinityTerm?: PodAffinityTermInfo
}

export interface PodAffinityTermInfo {
  labelSelector?: LabelSelectorInfo
  topologyKey?: string
  namespaces?: string[]
}

export interface LabelSelectorInfo {
  matchLabels?: Record<string, string>
}

export interface StatefulSetInfo {
  name: string
  namespace: string
  replicas: number
  readyReplicas: number
  updatedReplicas?: number
  age: string
  images: string[]
  serviceName: string
  conditions?: ConditionInfo[]
  status?: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  selector?: string
  nodeSelector?: string
  podAntiAffinity?: PodAntiAffinityInfo
  revisions?: RevisionInfo[]
}

export interface ReplicaSetInfo {
  name: string
  namespace: string
  desired: number
  current: number
  ready: number
  age: string
  images: string[]
}

export interface MatchExpression {
  key: string
  operator: string
  values?: string[]
}

export interface NodeSelectorTerm {
  matchExpressions: MatchExpression[]
}

export interface NodeSelectorInfo {
  nodeSelectorTerms: NodeSelectorTerm[]
}

export interface PreferredNodeSelector {
  weight: number
  preference: NodeSelectorTerm
}

export interface NodeAffinityInfo {
  requiredDuringSchedulingIgnoredDuringExecution?: NodeSelectorInfo
  preferredDuringSchedulingIgnoredDuringExecution?: PreferredNodeSelector[]
}

export interface AffinityInfo {
  nodeAffinity?: NodeAffinityInfo
}

export interface DaemonSetInfo {
  name: string
  namespace: string
  desired?: number
  current?: number
  ready?: number
  available?: number
  desiredNumberScheduled?: number
  currentNumberScheduled?: number
  numberReady?: number
  numberAvailable?: number
  age: string
  images?: string[]
  status?: string
  created?: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  selector?: string
  nodeSelector?: string
  strategy?: string
  conditions?: ConditionInfo[]
  tolerations?: TolerationInfo[]
  affinity?: AffinityInfo
}

export interface JobInfo {
  name: string
  namespace: string
  completions: string
  succeeded: number
  failed: number
  active: number
  duration: string
  age: string
  status: string
}

export interface CronJobInfo {
  name: string
  namespace: string
  schedule: string
  suspend: boolean
  active: number
  lastSchedule: string
  nextExecution: string
  timeZone: string
  age: string
}

export interface CronJobDetail extends CronJobInfo {
  labels?: Record<string, string>
  annotations?: Record<string, string>
}

export interface ServiceInfo {
  name: string
  namespace: string
  type: string
  clusterIP: string
  externalIP: string
  ports: string[]
  age: string
  labels?: Record<string, string>
  selector?: Record<string, string>
  status: string
}

export interface IngressInfo {
  name: string
  namespace: string
  class: string
  hosts: string[]
  paths: string
  address: string
  age: string
  tls: boolean
}

export interface NodeInfo {
  name: string
  status: string
  roles: string
  version: string
  os: string
  arch: string
  cpuCapacity: string
  memoryCapacity: string
  internalIP: string
  age: string
}

export interface EventInfo {
  type: string
  reason: string
  message: string
  object: string
  count: number
  age: string
  source: string
  namespace: string
}

export interface ConfigMapInfo {
  name: string
  namespace: string
  dataCount: number
  age: string
  labels: any
  annotations: Record<string, string>
  data: Record<string, string>
}

export interface SecretInfo {
  name: string
  namespace: string
  type: string
  dataCount: number
  age: string
  keys: string[]
}

export interface PVInfo {
  name: string
  capacity: string
  accessModes: string
  reclaimPolicy: string
  status: string
  claim: string
  claimNamespace: string
  claimName: string
  storageClass: string
  volumeMode: string
    provider?: { source: string; fields: { name: string; value: string }[] } | null
  age: string
  created: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  finalizers?: string[]
}

export interface StorageClassInfo {
  name: string
  provisioner: string
  reclaimPolicy: string
  volumeBindingMode: string
  allowVolumeExpansion: boolean
  default: boolean
  mountOptions: string[]
  parameters: Record<string, string>
  volumes: StorageClassVolume[]
  finalizers?: string[]
  labels?: Record<string, string>
  annotations?: Record<string, string>
  age: string
  created: string
}

export interface StorageClassVolume {
  name: string
  capacity: string
  status: string
}

export interface PVCInfo {
  name: string
  namespace: string
  status: string
  volume: string
  capacity: string
  requested: string
  accessModes: string
  storageClass: string
  volumeMode: string
  age: string
  created: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  finalizers?: string[]
  pods: string[]
  selector?: { matchLabels?: Record<string, string>; matchExpressions?: MatchExpression[] } | null
}

export interface PodMetricsInfo {
  name: string
  namespace: string
  containers: ContainerMetricsInfo[]
  totalCpu: string
  totalMem: string
}

export interface ContainerMetricsInfo {
  name: string
  cpu: string
  memory: string
}

export interface NodeMetricsInfo {
  name: string
  cpuUsage: string
  memUsage: string
  cpuPct: number
  memPct: number
}

export interface RevisionInfo {
  revision: string
  name: string
  replicas: number
  ready: number
  age: string
  images: string
}

export interface PortForwardSession {
  id: string
  namespace: string
  pod: string
  service?: string
  servicePort?: number
  localPort: number
  podPort: number
  status: string
  error?: string
  name: string
  target: string
  /** local — порт на localhost; proxy — через прокси сервера (localPort = 0) */
  mode: string
  scheme?: string
}

export interface ServiceDetail {
  name: string
  namespace: string
  type: string
  clusterIP: string
  clusterIPs: string[]
  externalIPs: string[]
  ipFamilies: string
  ipFamilyPolicy: string
  sessionAffinity: string
  ports: ServicePortInfo[]
  selector: Record<string, string>
  labels: Record<string, string>
  annotations: Record<string, string>
  finalizers: string[]
  age: string
  endpoints: EndpointInfo[]
  endpointSlices: EndpointSliceInfo[]
}

export interface ServicePortInfo {
  name: string
  port: number
  targetPort: string
  protocol: string
  nodePort: number
  forwarded: boolean
  localPort: number
}

export interface EndpointInfo {
  name: string
  addresses: string[]
}

export interface EndpointSliceInfo {
  name: string
  namespace: string
  addresses: string[]
  ports: string[]
  zone: string
  endpoints: string
  addressType: string
  age: string
}

export interface ResourceQuotaInfo {
  name: string
  namespace: string
  age: string
  cpuLimit: string
  cpuUsed: string
  memLimit: string
  memUsed: string
}

export interface LimitRangeInfo {
  name: string
  namespace: string
  age: string
  items: number
}

export interface HPAInfo {
  name: string
  namespace: string
  reference: string
  minReplicas: number
  maxReplicas: number
  currentReplicas: number
  desiredReplicas: number
  age: string
}

export interface PDBInfo {
  name: string
  namespace: string
  minAvailable: string
  maxUnavailable: string
  disruptionsAllowed: number
  currentHealthy: number
  desiredHealthy: number
  age: string
}

export interface PriorityClassInfo {
  name: string
  value: number
  age: string
}

export interface RuntimeClassInfo {
  name: string
  handler: string
  age: string
}

export interface LeaseInfo {
  name: string
  namespace: string
  holder: string
  age: string
}

export interface EndpointListInfo {
  name: string
  namespace: string
  addresses: number
  ports: number
  age: string
}

export interface NetworkPolicyInfo {
  name: string
  namespace: string
  podSelector: string
  age: string
}

export interface IngressClassInfo {
  name: string
  controller: string
  age: string
}

export interface PortForwardingInfo {
  id: string
  namespace: string
  pod: string
  localPort: number
  podPort: number
  status: string
}

export interface StorageClassInfo {
  name: string
  provisioner: string
  reclaimPolicy: string
  default: boolean
  age: string
}

export interface ServiceAccount {
  apiVersion?: string
  kind?: string
  metadata?: {
    name?: string
    namespace?: string
    creationTimestamp?: string
  }
  secrets?: Array<{
    name: string
  }>
  automountServiceAccountToken?: boolean
}

export interface Role {
  apiVersion?: string
  kind?: string
  metadata?: {
    name?: string
    namespace?: string
    creationTimestamp?: string
  }
  rules?: Array<{
    verbs: string[]
    apiGroups?: string[]
    resources?: string[]
    resourceNames?: string[]
  }>
}

export interface RoleBinding {
  apiVersion?: string
  kind?: string
  metadata?: {
    name?: string
    namespace?: string
    creationTimestamp?: string
  }
  roleRef?: {
    apiGroup: string
    kind: string
    name: string
  }
  subjects?: Array<{
    apiGroup?: string
    kind: string
    name: string
    namespace?: string
  }>
}

export interface ClusterRole {
  apiVersion?: string
  kind?: string
  metadata?: {
    name?: string
    creationTimestamp?: string
  }
  rules?: Array<{
    verbs: string[]
    apiGroups?: string[]
    resources?: string[]
    resourceNames?: string[]
  }>
}

export interface ClusterRoleBinding {
  apiVersion?: string
  kind?: string
  metadata?: {
    name?: string
    creationTimestamp?: string
  }
  roleRef?: {
    apiGroup: string
    kind: string
    name: string
  }
  subjects?: Array<{
    apiGroup?: string
    kind: string
    name: string
    namespace?: string
  }>
}

/** Команда (Go: auth.Team + роль текущего пользователя, app.TeamView) */
export interface Team {
  id: string
  name: string
  members: number
  clusters: number
  /** Роль текущего пользователя в команде; '' — не член (администратор организации видит все команды) */
  myRole: string
}

/** Участник команды (Go: auth.Member) */
export interface TeamMember {
  userId: string
  username: string
  role: string
}

/** Пользователь для добавления в команду (Go: auth.UserBrief) */
export interface UserBrief {
  id: string
  username: string
}
