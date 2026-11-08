// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { managedByOf } from './common'

type Case = { name: string; labels?: Record<string, string>; annotations?: Record<string, string>; want: string }

// Та же таблица, что managedByCases в internal/k8s/configmeta_test.go. Менять только вместе.
const cases: Case[] = [
  { name: 'none', want: '' },
  { name: 'argo tracking id', annotations: { 'argocd.argoproj.io/tracking-id': 'shop:apps/Deployment:prod/web' }, want: 'Application/shop' },
  { name: 'argo instance label', labels: { 'argocd.argoproj.io/instance': 'shop' }, want: 'Application/shop' },
  { name: 'argo tracking id without app', annotations: { 'argocd.argoproj.io/tracking-id': ':apps/Deployment:prod/web' }, want: '' },
  { name: 'flux kustomization', labels: { 'kustomize.toolkit.fluxcd.io/name': 'apps', 'kustomize.toolkit.fluxcd.io/namespace': 'flux-system' }, want: 'Kustomization/apps' },
  { name: 'flux kustomization name only', labels: { 'kustomize.toolkit.fluxcd.io/name': 'apps' }, want: 'Kustomization/apps' },
  { name: 'flux helmrelease', labels: { 'helm.toolkit.fluxcd.io/name': 'podinfo', 'helm.toolkit.fluxcd.io/namespace': 'apps' }, want: 'HelmRelease/podinfo' },
  { name: 'flux empty name', labels: { 'kustomize.toolkit.fluxcd.io/name': '', 'helm.toolkit.fluxcd.io/name': '' }, want: '' },
  { name: 'flux namespace label alone', labels: { 'kustomize.toolkit.fluxcd.io/namespace': 'flux-system' }, want: '' },
  { name: 'kustomization before helmrelease', labels: { 'kustomize.toolkit.fluxcd.io/name': 'apps', 'helm.toolkit.fluxcd.io/name': 'podinfo' }, want: 'Kustomization/apps' },
  { name: 'helm', labels: { 'app.kubernetes.io/managed-by': 'Helm' }, annotations: { 'meta.helm.sh/release-name': 'db' }, want: 'Helm/db' },
  { name: 'helm without release', labels: { 'app.kubernetes.io/managed-by': 'Helm' }, want: '' },
  { name: 'helm installed by flux', labels: { 'app.kubernetes.io/managed-by': 'Helm', 'helm.toolkit.fluxcd.io/name': 'podinfo' }, annotations: { 'meta.helm.sh/release-name': 'podinfo' }, want: 'HelmRelease/podinfo' },
  { name: 'argo before flux', labels: { 'argocd.argoproj.io/instance': 'shop', 'kustomize.toolkit.fluxcd.io/name': 'apps' }, want: 'Application/shop' },
  { name: 'argo before flux and helm', labels: { 'argocd.argoproj.io/instance': 'shop', 'helm.toolkit.fluxcd.io/name': 'p', 'app.kubernetes.io/managed-by': 'Helm' }, annotations: { 'meta.helm.sh/release-name': 'p' }, want: 'Application/shop' },
]

describe('managedByOf', () => {
  for (const c of cases) {
    it(c.name, () => {
      // Ресурсы приходят в двух формах: плоской (labels на верхнем уровне) и k8s (metadata.labels).
      expect(managedByOf({ labels: c.labels, annotations: c.annotations })).toBe(c.want)
      expect(managedByOf({ metadata: { labels: c.labels, annotations: c.annotations } })).toBe(c.want)
    })
  }

  it('готовое поле managedBy от бэкенда главнее меток', () => {
    expect(managedByOf({ managedBy: 'Helm/x', labels: { 'kustomize.toolkit.fluxcd.io/name': 'apps' } })).toBe('Helm/x')
  })

  it('не падает на пустых и неполных объектах', () => {
    for (const r of [undefined, null, {}, { labels: null }, { metadata: {} }]) expect(managedByOf(r)).toBe('')
  })
})
