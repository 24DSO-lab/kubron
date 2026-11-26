// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/opsplatform"
	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
)

// matrixVirtualKinds — виды матрицы, которые нельзя прочитать get/list: у них нет хранимых объектов («Review», подресурсы,
// шаблоны) либо чтение не имеет смысла для проверки совместимости. В отчёте они остаются «не проверено» (coverage).
var matrixVirtualKinds = []schema.GroupKind{
	{Group: "admission.k8s.io", Kind: "AdmissionReview"},
	{Group: "apiextensions.k8s.io", Kind: "ConversionReview"},
	{Group: "apidiscovery.k8s.io", Kind: "APIGroupDiscovery"},
	{Group: "apps", Kind: "DeploymentRollback"},
	{Group: "apps", Kind: "Scale"},
	{Group: "extensions", Kind: "DeploymentRollback"},
	{Group: "extensions", Kind: "Scale"},
	{Group: "authentication.k8s.io", Kind: "SelfSubjectReview"},
	{Group: "authentication.k8s.io", Kind: "TokenReview"},
	{Group: "authorization.k8s.io", Kind: "LocalSubjectAccessReview"},
	{Group: "authorization.k8s.io", Kind: "SelfSubjectAccessReview"},
	{Group: "authorization.k8s.io", Kind: "SelfSubjectRulesReview"},
	{Group: "authorization.k8s.io", Kind: "SubjectAccessReview"},
	{Group: "batch", Kind: "JobTemplate"},
	{Group: "lifecycle.k8s.io", Kind: "Eviction"},
	{Group: "lifecycle.k8s.io", Kind: "EvictionRequest"},
	{Group: "policy", Kind: "Eviction"},
}

// Страж (ADR-0025 ↔ ADR-0026): каждый вид встроенной матрицы либо читается платформой, либо явно назван нечитаемым.
// Перегенерация матрицы с новым видом краснит этот тест, а не молча расширяет «не проверено».
func TestAllowlistCoversReadinessMatrix(t *testing.T) {
	m, err := apimatrix.Default()
	if err != nil {
		t.Fatal(err)
	}
	allowed := opsplatform.AllowedKinds()
	seen := map[schema.GroupKind]bool{}
	for _, e := range m.Entries {
		gk := schema.GroupKind{Group: e.Group, Kind: e.Kind}
		if seen[gk] {
			continue
		}
		seen[gk] = true
		inAllow, virtual := slices.Contains(allowed, gk), slices.Contains(matrixVirtualKinds, gk)
		switch {
		case inAllow && virtual:
			t.Errorf("%v is both readable and listed as virtual", gk)
		case !inAllow && !virtual:
			t.Errorf("%v is in the readiness matrix but neither in the opsplatform allowlist nor in matrixVirtualKinds", gk)
		}
	}
	for _, gk := range matrixVirtualKinds {
		if !seen[gk] {
			t.Errorf("matrixVirtualKinds lists %v, which is not in the matrix any more", gk)
		}
	}
}
