// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// managedByCases — общая таблица для Go и TypeScript: тот же набор лежит в
// frontend/src/resources/definitions/common.test.ts. Менять только вместе.
var managedByCases = []struct {
	name        string
	labels      map[string]string
	annotations map[string]string
	want        string
}{
	{name: "none", want: ""},
	{name: "argo tracking id", annotations: map[string]string{"argocd.argoproj.io/tracking-id": "shop:apps/Deployment:prod/web"}, want: "Application/shop"},
	{name: "argo instance label", labels: map[string]string{"argocd.argoproj.io/instance": "shop"}, want: "Application/shop"},
	{name: "argo tracking id without app", annotations: map[string]string{"argocd.argoproj.io/tracking-id": ":apps/Deployment:prod/web"}, want: ""},
	{name: "flux kustomization", labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, want: "Kustomization/apps"},
	{name: "flux kustomization name only", labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps"}, want: "Kustomization/apps"},
	{name: "flux helmrelease", labels: map[string]string{"helm.toolkit.fluxcd.io/name": "podinfo", "helm.toolkit.fluxcd.io/namespace": "apps"}, want: "HelmRelease/podinfo"},
	{name: "flux empty name", labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "", "helm.toolkit.fluxcd.io/name": ""}, want: ""},
	{name: "flux namespace label alone", labels: map[string]string{"kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, want: ""},
	{name: "kustomization before helmrelease", labels: map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "helm.toolkit.fluxcd.io/name": "podinfo"}, want: "Kustomization/apps"},
	{name: "helm", labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, annotations: map[string]string{"meta.helm.sh/release-name": "db"}, want: "Helm/db"},
	{name: "helm without release", labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, want: ""},
	{name: "helm installed by flux", labels: map[string]string{"app.kubernetes.io/managed-by": "Helm", "helm.toolkit.fluxcd.io/name": "podinfo"}, annotations: map[string]string{"meta.helm.sh/release-name": "podinfo"}, want: "HelmRelease/podinfo"},
	{name: "argo before flux", labels: map[string]string{"argocd.argoproj.io/instance": "shop", "kustomize.toolkit.fluxcd.io/name": "apps"}, want: "Application/shop"},
	{name: "argo before flux and helm", labels: map[string]string{"argocd.argoproj.io/instance": "shop", "helm.toolkit.fluxcd.io/name": "p", "app.kubernetes.io/managed-by": "Helm"}, annotations: map[string]string{"meta.helm.sh/release-name": "p"}, want: "Application/shop"},
}

func TestManagedByOf(t *testing.T) {
	for _, tc := range managedByCases {
		t.Run(tc.name, func(t *testing.T) {
			got := managedByOf(metav1.ObjectMeta{Labels: tc.labels, Annotations: tc.annotations})
			if got != tc.want {
				t.Errorf("managedByOf = %q, want %q", got, tc.want)
			}
		})
	}
}
