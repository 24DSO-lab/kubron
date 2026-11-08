// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import type { ResourceDefinition } from './types'
import { role } from './definitions/role'
import { roleBinding } from './definitions/rolebinding'
import { clusterRole } from './definitions/clusterrole'
import { clusterRoleBinding } from './definitions/clusterrolebinding'
import { serviceAccount } from './definitions/serviceaccount'
import { configMap } from './definitions/configmap'
import { namespaceResource } from './definitions/namespace'
import { replicaSet } from './definitions/replicaset'
import { daemonSet } from './definitions/daemonset'
import { deployment } from './definitions/deployment'
import { pod } from './definitions/pod'
import { service } from './definitions/service'
import { secret } from './definitions/secret'
import { job } from './definitions/job'
import { statefulSet } from './definitions/statefulset'
import { cronJob } from './definitions/cronjob'
import { ingress } from './definitions/ingress'
import { ingressClass } from './definitions/ingressclass'
import { networkPolicy } from './definitions/networkpolicy'
import { persistentVolume } from './definitions/pv'
import { persistentVolumeClaim } from './definitions/pvc'
import { storageClass } from './definitions/storageclass'
import { resourceQuota } from './definitions/resourcequota'
import { limitRange } from './definitions/limitrange'
import { horizontalPodAutoscaler } from './definitions/hpa'
import { podDisruptionBudget } from './definitions/pdb'
import { priorityClass } from './definitions/priorityclass'
import { runtimeClass } from './definitions/runtimeclass'
import { lease } from './definitions/lease'
import { mutatingWebhookConfiguration, validatingWebhookConfiguration } from './definitions/webhook'
import { endpoint } from './definitions/endpoint'
import { event } from './definitions/event'
import { node } from './definitions/node'
import { portForward } from './definitions/portforward'
import { helmRelease } from './definitions/helmrelease'
import { helmChart } from './definitions/helmchart'
import { endpointSlice } from './definitions/endpointslice'
import { argoApplication } from './definitions/argocd/application'
import { argoApplicationSet } from './definitions/argocd/applicationset'
import { argoProject } from './definitions/argocd/project'
import { argoRepository } from './definitions/argocd/repository'
import { argoCluster } from './definitions/argocd/cluster'
import { fluxKustomization } from './definitions/flux/kustomization'
import { fluxHelmRelease } from './definitions/flux/helmrelease'
import { fluxGitRepository } from './definitions/flux/source-gitrepository'
import { fluxOCIRepository } from './definitions/flux/source-ocirepository'
import { fluxHelmRepository } from './definitions/flux/source-helmrepository'
import { fluxHelmChart } from './definitions/flux/source-helmchart'
import { fluxBucket } from './definitions/flux/source-bucket'
import { fluxAlert } from './definitions/flux/alert'
import { fluxProvider } from './definitions/flux/provider'
import { fluxReceiver } from './definitions/flux/receiver'
import { fluxImageRepository } from './definitions/flux/imagerepository'
import { fluxImagePolicy } from './definitions/flux/imagepolicy'
import { fluxImageUpdateAutomation } from './definitions/flux/imageupdateautomation'
import { dynamicById, dynamicByView } from './dynamicRegistry'
import { customResourceDefinition } from './definitions/crd'
import { application } from './definitions/application'

/** Добавляя новый ресурс, достаточно дописать его сюда. */
export const resources: ResourceDefinition[] = [
  // Порядок задаёт порядок пунктов внутри групп бокового меню (см. navigation.ts)
  // workloads
  pod,
  deployment,
  daemonSet,
  statefulSet,
  replicaSet,
  job,
  cronJob,
  // config
  configMap,
  secret,
  resourceQuota,
  limitRange,
  horizontalPodAutoscaler,
  podDisruptionBudget,
  priorityClass,
  runtimeClass,
  lease,
  mutatingWebhookConfiguration,
  validatingWebhookConfiguration,
  // network
  service,
  endpoint,
  endpointSlice,
  ingress,
  ingressClass,
  networkPolicy,
  portForward,
  // storage
  persistentVolume,
  persistentVolumeClaim,
  storageClass,
  // helm
  helmRelease,
  helmChart,
  // argo cd
  argoApplication,
  argoApplicationSet,
  argoProject,
  argoRepository,
  argoCluster,
  // flux cd (id и view с префиксом flux-: реестр резолвит определения по id, дубликат молча перекроет чужое)
  fluxKustomization,
  fluxHelmRelease,
  fluxGitRepository,
  fluxOCIRepository,
  fluxHelmRepository,
  fluxHelmChart,
  fluxBucket,
  fluxAlert,
  fluxProvider,
  fluxReceiver,
  fluxImageRepository,
  fluxImagePolicy,
  fluxImageUpdateAutomation,
  // access control
  serviceAccount,
  role,
  roleBinding,
  clusterRole,
  clusterRoleBinding,
  // верхний уровень
  application,
  node,
  event,
  namespaceResource,
    // custom resources
  customResourceDefinition,
]

const byView = new Map(resources.map(r => [r.view, r]))
// Дубликат id молча перекрывает раннее определение (Map.set) — например, Lens-«Applications»
// так перекрыли Argo CD Application и спрятали его вкладки. В dev предупреждаем сразу.
const byId = new Map<string, ResourceDefinition>()
for (const r of resources) {
  if (import.meta.env.DEV && byId.has(r.id)) {
    console.warn(`duplicate resource id '${r.id}': '${byId.get(r.id)!.view}' will be overridden by '${r.view}'`)
  }
  byId.set(r.id, r)
}


export const getResourceByView = (view: string) => byView.get(view) ?? dynamicByView.get(view)
export const getResourceById = (id: string) => byId.get(id.toLowerCase()) ?? dynamicById.get(id.toLowerCase())