// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func fluxObj(apiVersion, kind, ns, name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"namespace": ns, "name": name},
	}}
	if spec != nil {
		u.Object["spec"] = spec
	}
	return u
}

func newFluxDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	kust, _ := fluxKindByName(FluxKindKustomization)
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		kust.GVR("v1"): "KustomizationList",
	}, objs...)
}

// fluxManager — Manager с активным подключением к fake dynamic client.
func fluxManager(dyn *dynamicfake.FakeDynamicClient) *Manager {
	m := managerWithBuiltConn("c", &ClusterConnection{Name: "c", DynamicClient: dyn})
	m.activeKey = "c"
	return m
}

type fluxListed struct{ ns, name, path string }

func parseFluxListed(u *unstructured.Unstructured) fluxListed {
	return fluxListed{ns: u.GetNamespace(), name: u.GetName(), path: strAt(u.Object, "spec", "path")}
}

func TestFluxListReadsThroughAPIAndFiltersNamespaces(t *testing.T) {
	const apiVersion = "kustomize.toolkit.fluxcd.io/v1"
	dyn := newFluxDyn(
		fluxObj(apiVersion, "Kustomization", "flux-system", "apps", map[string]any{"path": "./apps"}),
		fluxObj(apiVersion, "Kustomization", "team-a", "web", map[string]any{"path": "./web"}),
		fluxObj(apiVersion, "Kustomization", "team-b", "db", nil),
	)
	m := fluxManager(dyn)
	kust, _ := fluxKindByName(FluxKindKustomization)
	nsOf := func(i fluxListed) string { return i.ns }

	all, err := fluxList(m, kust, nil, parseFluxListed, nsOf)
	if err != nil || len(all) != 3 {
		t.Fatalf("all namespaces: err=%v items=%+v", err, all)
	}
	one, err := fluxList(m, kust, []string{"team-a"}, parseFluxListed, nsOf)
	if err != nil || !slices.Equal(one, []fluxListed{{"team-a", "web", "./web"}}) {
		t.Fatalf("team-a: err=%v items=%+v", err, one)
	}
	two, err := fluxList(m, kust, []string{"team-b", "flux-system"}, parseFluxListed, nsOf)
	if err != nil || len(two) != 2 {
		t.Fatalf("two namespaces: err=%v items=%+v", err, two)
	}
	// Нет объектов: пустой список, не ошибка.
	none, err := fluxList(m, kust, []string{"empty"}, parseFluxListed, nsOf)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty namespace: err=%v items=%+v", err, none)
	}
}

func TestFluxListRejectsBadScope(t *testing.T) {
	m := fluxManager(newFluxDyn())
	kust, _ := fluxKindByName(FluxKindKustomization)
	if _, err := fluxList(m, kust, []string{"Bad_Namespace!"}, parseFluxListed, func(i fluxListed) string { return i.ns }); err == nil {
		t.Fatal("the namespace set must be validated by parseScope")
	}
}

func TestFluxListUnsupportedVersion(t *testing.T) {
	m := fluxManager(newFluxDyn())
	// Вид с версией вне допустимых: таблица не отдаёт GVR, который не из allowlist.
	kind := fluxKind{Kind: "Kustomization", Group: fluxGroupKustomize, Plural: "kustomizations", Namespaced: true, Versions: []string{"v1beta9"}}
	_, err := fluxList(m, kind, nil, parseFluxListed, func(i fluxListed) string { return i.ns })
	if !errors.Is(err, errFluxVersionUnsupported) {
		t.Fatalf("err = %v, want errFluxVersionUnsupported", err)
	}
}

func TestFluxServedVersionsStubGivesPreferredVersions(t *testing.T) {
	got, err := fluxServedVersions(&ClusterConnection{})
	if err != nil || len(got) != len(fluxKinds) {
		t.Fatalf("err=%v versions=%v", err, got)
	}
	for _, k := range fluxKinds {
		if got[k.Kind] != k.Versions[0] {
			t.Errorf("%s: %q, expected the first allowed version %q", k.Kind, got[k.Kind], k.Versions[0])
		}
	}
}
