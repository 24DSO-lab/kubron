// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

const fluxgFixtures = "testdata/flux/inventory"

func fluxgFixture(t testing.TB, file string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fluxgFixtures, file))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func fluxgNode(g *FluxGraph, id string) *FluxGraphNode {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func fluxgEdge(g *FluxGraph, from, to, typ string) bool {
	return slices.Contains(g.Edges, FluxGraphEdge{From: from, To: to, Type: typ})
}

func fluxgGraph(t testing.TB, kube *fake.Clientset, dyn *dynamicfake.FakeDynamicClient, ref FluxRef) (*FluxGraph, error) {
	t.Helper()
	return fluxManager(dyn).getFluxGraph(ref, kube)
}

func fluxgGitRepo(ns, name string, ready bool) *unstructured.Unstructured {
	u := fluxObj("source.toolkit.fluxcd.io/v1", "GitRepository", ns, name, nil)
	status := "False"
	if ready {
		status = "True"
	}
	u.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": status}}}
	return u
}

func fluxgKust(ns, name string, deps ...string) *unstructured.Unstructured {
	var d []any
	for _, n := range deps {
		d = append(d, map[string]any{"name": n})
	}
	spec := map[string]any{}
	if d != nil {
		spec["dependsOn"] = d
	}
	return fluxObj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", ns, name, spec)
}

// ─── парсер id ───

func TestParseFluxInventoryID(t *testing.T) {
	cases := []struct {
		id   string
		want fluxInventoryEntry
		ok   bool
	}{
		{"prod_web_apps_Deployment", fluxInventoryEntry{"prod", "web", "apps", "Deployment", ""}, true},
		{"prod_web__Service", fluxInventoryEntry{"prod", "web", "", "Service", ""}, true},
		{"_prod__Namespace", fluxInventoryEntry{"", "prod", "", "Namespace", ""}, true},
		{"_system__controller__reader_rbac.authorization.k8s.io_ClusterRole",
			fluxInventoryEntry{"", "system:controller:reader", "rbac.authorization.k8s.io", "ClusterRole", ""}, true},
		{"flux-system_apps_kustomize.toolkit.fluxcd.io_Kustomization",
			fluxInventoryEntry{"flux-system", "apps", "kustomize.toolkit.fluxcd.io", "Kustomization", ""}, true},
		{"_widgets_apiextensions.k8s.io_CustomResourceDefinition",
			fluxInventoryEntry{"", "widgets", "apiextensions.k8s.io", "CustomResourceDefinition", ""}, true},
		{"ns_a__b_c_Kind", fluxInventoryEntry{"ns", "a:b", "c", "Kind", ""}, true},

		{"", fluxInventoryEntry{}, false},
		{"nounderscore", fluxInventoryEntry{}, false},
		{"one_two", fluxInventoryEntry{}, false},
		{"ns_name_group_", fluxInventoryEntry{}, false},
		{"ns__group_Kind", fluxInventoryEntry{}, false},
		{"_", fluxInventoryEntry{}, false},
		{"___", fluxInventoryEntry{}, false},
		{"ns_name_gr oup_Kind", fluxInventoryEntry{}, false},
		{"ns_name_group_Ki nd", fluxInventoryEntry{}, false},
	}
	for _, tc := range cases {
		got, ok := parseFluxInventoryID(tc.id)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%q: got %+v ok=%v, want %+v ok=%v", tc.id, got, ok, tc.want, tc.ok)
		}
	}
}

func FuzzParseFluxInventoryID(f *testing.F) {
	for _, s := range []string{"", "_", "__", "___", "a_b_c_D", "_a__K", "ns_name__ConfigMap", "a__b__c", "ü_ñ_é_ß", "\x00_\xff_\x00_\x00", strings.Repeat("_", 64)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, id string) {
		e, ok := parseFluxInventoryID(id)
		if !ok {
			return
		}
		// Разобранная запись всегда пригодна для построения графа: имя и вид непусты, пробелов в виде и группе нет.
		if e.Name == "" || e.Kind == "" || strings.ContainsAny(e.Kind+e.Group, " \t\r\n") {
			t.Fatalf("%q parsed to an unusable entry %+v", id, e)
		}
		// Части не содержат разделителя, кроме имени (его «_» допустим).
		if strings.Contains(e.Namespace+e.Group+e.Kind, "_") && strings.Count(id, "_") < 3 {
			t.Fatalf("%q: %+v", id, e)
		}
	})
}

func TestFluxInventoryFromFixtures(t *testing.T) {
	entries, present, skipped := fluxInventory(fluxgFixture(t, "kustomization-v1.yaml").Object)
	if !present || skipped != 0 || len(entries) != 7 {
		t.Fatalf("present=%v skipped=%d entries=%+v", present, skipped, entries)
	}
	if entries[0].Version != "v1" || entries[5].Name != "system:controller:reader" || entries[5].Namespace != "" {
		t.Fatalf("entries: %+v", entries)
	}

	entries, present, skipped = fluxInventory(fluxgFixture(t, "kustomization-junk-inventory.yaml").Object)
	if !present || len(entries) != 1 || entries[0].Name != "cm" || entries[0].Version != "" || skipped != 8 {
		t.Fatalf("junk: present=%v skipped=%d entries=%+v (duplicates collapse, bad ids are skipped)", present, skipped, entries)
	}

	if _, present, _ := fluxInventory(fluxgFixture(t, "helmrelease-no-inventory.yaml").Object); present {
		t.Fatal("no status.inventory means present=false")
	}
	if entries, present, _ := fluxInventory(fluxgFixture(t, "kustomization-empty-inventory.yaml").Object); present || len(entries) != 0 {
		t.Fatal("no status.inventory in fresh object")
	}
	// Мусор вместо inventory не паникует.
	for _, inv := range []any{"x", int64(1), []any{}, map[string]any{"entries": "x"}, map[string]any{"entries": map[string]any{}}} {
		obj := map[string]any{"status": map[string]any{"inventory": inv}}
		_, _, _ = fluxInventory(obj)
	}
	_, _, _ = fluxInventory(nil)
}

// ─── граф ───

// fluxgKube — Deployment web (в inventory) с ReplicaSet и Pod, Service и Ingress в namespace ns.
func fluxgKube(ns string) *fake.Clientset {
	t := true
	owner := func(kind, name string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: kind, Name: name, Controller: &t}}
	}
	return fake.NewSimpleClientset(
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-abc", OwnerReferences: owner("Deployment", "web")}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web-abc-1", Labels: map[string]string{"app": "web"}, OwnerReferences: owner("ReplicaSet", "web-abc")},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "web"}}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web"}, Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "web"}}}},
			}}}},
		}},
	)
}

func TestFluxGraphKustomizationChain(t *testing.T) {
	root := fluxgFixture(t, "kustomization-v1.yaml")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		root,
		fluxgGitRepo("flux-system", "fleet", true),
		fluxgKust("flux-system", "infra"),
	)
	g, err := fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "apps"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Available || g.Remote || g.Truncated {
		t.Fatalf("flags: %+v", g)
	}

	const kg = "kustomize.toolkit.fluxcd.io"
	rootID := graphID(kg, "Kustomization", "flux-system", "apps")
	srcID := graphID("source.toolkit.fluxcd.io", "GitRepository", "flux-system", "fleet")
	depID := graphID(kg, "Kustomization", "flux-system", "infra")

	// Вершины Flux: роль, состояние, версия API.
	if n := fluxgNode(g, rootID); n == nil || n.Role != FluxRoleFlux || n.State != string(FluxStateReady) || n.Version != "v1" || n.Group != kg {
		t.Fatalf("root: %+v", n)
	}
	if n := fluxgNode(g, srcID); n == nil || n.Role != FluxRoleSource || n.State != string(FluxStateReady) {
		t.Fatalf("source: %+v", n)
	}
	if n := fluxgNode(g, depID); n == nil || n.Role != FluxRoleFlux || n.State != string(FluxStateUnknown) {
		t.Fatalf("dependency: %+v", n)
	}
	// Рёбра Flux: источник → объект, зависимость → зависимый, объект → ресурсы из inventory.
	if !fluxgEdge(g, srcID, rootID, FluxEdgeSourceRef) || !fluxgEdge(g, depID, rootID, FluxEdgeDependsOn) {
		t.Fatalf("edges: %+v", g.Edges)
	}

	dep := graphID("apps", "Deployment", "prod", "web")
	rs := graphID("apps", "ReplicaSet", "prod", "web-abc")
	pod := graphID("", "Pod", "prod", "web-abc-1")
	svc := graphID("", "Service", "prod", "web")
	ing := graphID("networking.k8s.io", "Ingress", "prod", "web")
	cm := graphID("", "ConfigMap", "prod", "settings")
	for _, id := range []string{dep, svc, ing, cm, graphID("", "Namespace", "", "prod"),
		graphID("rbac.authorization.k8s.io", "ClusterRole", "", "system:controller:reader")} {
		n := fluxgNode(g, id)
		if n == nil || n.Role != FluxRoleResource || !fluxgEdge(g, rootID, id, FluxEdgeInventory) {
			t.Fatalf("inventory resource %s: node=%+v", id, n)
		}
	}
	// Версия из поля v записи inventory.
	if n := fluxgNode(g, dep); n.Version != "v1" {
		t.Fatalf("version: %+v", n)
	}
	// Связи общего построителя: owns, selects, routes. ReplicaSet и Pod найдены по связям: ребра inventory у них нет.
	for _, e := range []FluxGraphEdge{{dep, rs, FluxEdgeOwns}, {rs, pod, FluxEdgeOwns}, {svc, pod, FluxEdgeSelects}, {ing, svc, FluxEdgeRoutes}} {
		if !slices.Contains(g.Edges, e) {
			t.Errorf("missing edge %+v", e)
		}
	}
	if n := fluxgNode(g, pod); n == nil || n.Role != FluxRoleResource || n.State != "Healthy" || fluxgEdge(g, rootID, pod, FluxEdgeInventory) {
		t.Fatalf("pod: %+v", n)
	}
	// Kustomization из inventory: вершина ресурса, но не потеря состояния вершины Flux (здесь её нет — это просто ресурс).
	if n := fluxgNode(g, graphID(kg, "Kustomization", "flux-system", "extra")); n == nil || n.Role != FluxRoleResource {
		t.Fatalf("nested kustomization: %+v", n)
	}
	// Предупреждений нет, а срезы никогда не nil.
	if len(g.Warnings) != 0 || g.Nodes == nil || g.Edges == nil || g.Warnings == nil {
		t.Fatalf("warnings=%v", g.Warnings)
	}
}

func TestFluxGraphFluxNodeWinsOverResourceWithSameID(t *testing.T) {
	// inventory родителя содержит дочернюю Kustomization, которая одновременно является его зависимостью.
	parent := fluxgFixture(t, "kustomization-v1.yaml")
	// infra встречается и в dependsOn, и в inventory (подменяем запись).
	entries, _, _ := unstructured.NestedSlice(parent.Object, "status", "inventory", "entries")
	entries = append(entries, map[string]any{"id": "flux-system_infra_kustomize.toolkit.fluxcd.io_Kustomization", "v": "v1"})
	_ = unstructured.SetNestedSlice(parent.Object, entries, "status", "inventory", "entries")
	dyn = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), parent,
		fluxgGitRepo("flux-system", "fleet", true), fluxgKust("flux-system", "infra"))

	g, err := fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "apps"})
	if err != nil {
		t.Fatal(err)
	}
	id := graphID("kustomize.toolkit.fluxcd.io", "Kustomization", "flux-system", "infra")
	if n := fluxgNode(g, id); n == nil || n.Role != FluxRoleFlux {
		t.Fatalf("the Flux node must win: %+v", n)
	}
	rootID := graphID("kustomize.toolkit.fluxcd.io", "Kustomization", "flux-system", "apps")
	if !fluxgEdge(g, id, rootID, FluxEdgeDependsOn) || !fluxgEdge(g, rootID, id, FluxEdgeInventory) {
		t.Fatalf("both edges are kept: %+v", g.Edges)
	}
	count := 0
	for _, n := range g.Nodes {
		if n.ID == id {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("node duplicated %d times", count)
	}
}

func TestFluxGraphHelmRelease(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		fluxgFixture(t, "helmrelease-v2.8.yaml"),
		fluxObj("source.toolkit.fluxcd.io/v1", "HelmRepository", "apps", "charts", nil),
	)
	g, err := fluxgGraph(t, fluxgKube("apps"), dyn, FluxRef{FluxKindHelmRelease, "apps", "podinfo"})
	if err != nil {
		t.Fatal(err)
	}
	rootID := graphID("helm.toolkit.fluxcd.io", "HelmRelease", "apps", "podinfo")
	src := graphID("source.toolkit.fluxcd.io", "HelmRepository", "apps", "charts")
	if !g.Available || !fluxgEdge(g, src, rootID, FluxEdgeSourceRef) ||
		!fluxgEdge(g, rootID, graphID("apps", "Deployment", "apps", "podinfo"), FluxEdgeInventory) ||
		!fluxgEdge(g, rootID, graphID("", "Service", "apps", "podinfo"), FluxEdgeInventory) {
		t.Fatalf("graph: %+v", g)
	}
	raw, _ := json.Marshal(g)
	if strings.Contains(string(raw), "S3CRET") {
		t.Fatalf("values leaked: %s", raw)
	}
}

func TestFluxGraphHelmReleaseChartRef(t *testing.T) {
	hr := fluxObj("helm.toolkit.fluxcd.io/v2", "HelmRelease", "apps", "web", map[string]any{
		"chartRef": map[string]any{"kind": "OCIRepository", "name": "chart", "namespace": "oci"}})
	hr.Object["status"] = map[string]any{"inventory": map[string]any{"entries": []any{}}}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), hr,
		fluxObj("source.toolkit.fluxcd.io/v1", "OCIRepository", "oci", "chart", nil))
	g, err := fluxgGraph(t, fluxgKube("apps"), dyn, FluxRef{FluxKindHelmRelease, "apps", "web"})
	if err != nil || !g.Available {
		t.Fatalf("err=%v g=%+v", err, g)
	}
	src := graphID("source.toolkit.fluxcd.io", "OCIRepository", "oci", "chart")
	if !fluxgEdge(g, src, graphID("helm.toolkit.fluxcd.io", "HelmRelease", "apps", "web"), FluxEdgeSourceRef) {
		t.Fatalf("edges: %+v", g.Edges)
	}
}

func TestFluxGraphHelmReleaseWithoutInventoryIsUnavailable(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fluxgFixture(t, "helmrelease-no-inventory.yaml"))
	g, err := fluxgGraph(t, fluxgKube("apps"), dyn, FluxRef{FluxKindHelmRelease, "apps", "old"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Available || g.Remote || len(g.Nodes) != 0 || len(g.Edges) != 0 || g.Nodes == nil || g.Edges == nil || g.Warnings == nil {
		t.Fatalf("an old HelmRelease must give an unavailable graph: %+v", g)
	}
}

func TestFluxGraphRemoteHasFluxNodesOnly(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		fluxgFixture(t, "kustomization-remote.yaml"), fluxgGitRepo("flux-system", "fleet", true))
	kube := fluxgKube("prod")
	g, err := fluxgGraph(t, kube, dyn, FluxRef{FluxKindKustomization, "flux-system", "remote"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Available || !g.Remote {
		t.Fatalf("flags: %+v", g)
	}
	for _, n := range g.Nodes {
		if n.Role == FluxRoleResource {
			t.Fatalf("a remote object must not resolve resources locally: %+v", n)
		}
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 || g.Edges[0].Type != FluxEdgeSourceRef {
		t.Fatalf("graph: %+v", g)
	}
	// В локальный кластер за ресурсами не ходили вообще: у чужого кластера те же имена означали бы чужие объекты.
	for _, a := range kube.Actions() {
		t.Fatalf("the local cluster must not be queried for a remote object: %v", a)
	}
}

func TestFluxGraphEmptyInventoryAndJunk(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		fluxgFixture(t, "kustomization-empty-inventory.yaml"), fluxgFixture(t, "kustomization-junk-inventory.yaml"),
		fluxgGitRepo("flux-system", "fleet", true))
	g, err := fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "fresh"})
	if err != nil || !g.Available || len(g.Nodes) != 2 {
		t.Fatalf("fresh: err=%v g=%+v", err, g)
	}
	g, err = fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "junk"})
	if err != nil || !g.Available {
		t.Fatalf("junk: err=%v", err)
	}
	if fluxgNode(g, graphID("", "ConfigMap", "ok", "cm")) == nil {
		t.Fatalf("the valid entry must be kept: %+v", g.Nodes)
	}
	if len(g.Warnings) != 1 || !strings.Contains(g.Warnings[0], "8 inventory entries") {
		t.Fatalf("skipped entries must be reported once: %v", g.Warnings)
	}
}

func TestFluxGraphDependsOnCycleAndDepth(t *testing.T) {
	// a → b → c → a (цикл), плюс самозависимость и зависимость в другом namespace.
	a := fluxObj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "ns", "a", map[string]any{"dependsOn": []any{
		map[string]any{"name": "b"}, map[string]any{"name": "a"}, map[string]any{"name": "far", "namespace": "other"}}})
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), a,
		fluxgKust("ns", "b", "c"), fluxgKust("ns", "c", "a"), fluxgKust("other", "far"))
	g, err := fluxgGraph(t, fluxgKube("ns"), dyn, FluxRef{FluxKindKustomization, "ns", "a"})
	if err != nil {
		t.Fatal(err)
	}
	id := func(ns, n string) string { return graphID("kustomize.toolkit.fluxcd.io", "Kustomization", ns, n) }
	if len(g.Nodes) != 4 {
		t.Fatalf("nodes: %+v", g.Nodes)
	}
	for _, e := range []FluxGraphEdge{
		{id("ns", "b"), id("ns", "a"), FluxEdgeDependsOn}, {id("ns", "c"), id("ns", "b"), FluxEdgeDependsOn},
		{id("ns", "a"), id("ns", "c"), FluxEdgeDependsOn}, {id("other", "far"), id("ns", "a"), FluxEdgeDependsOn},
	} {
		if !slices.Contains(g.Edges, e) {
			t.Errorf("missing %+v", e)
		}
	}
	for _, e := range g.Edges {
		if e.From == e.To {
			t.Fatalf("self-edge: %+v", e)
		}
	}
}

func TestFluxGraphDependsOnLimit(t *testing.T) {
	// Цепочка длиннее предела: обрезается, граф остаётся связным, Truncated=true.
	var objs []runtime.Object
	n := fluxGraphMaxFlux + 20
	for i := 0; i < n; i++ {
		var deps []string
		if i+1 < n {
			deps = []string{fmt.Sprintf("k%d", i+1)}
		}
		objs = append(objs, fluxgKust("ns", fmt.Sprintf("k%d", i), deps...))
	}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)
	g, err := fluxgGraph(t, fluxgKube("ns"), dyn, FluxRef{FluxKindKustomization, "ns", "k0"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Truncated || len(g.Nodes) != fluxGraphMaxFlux {
		t.Fatalf("truncated=%v nodes=%d", g.Truncated, len(g.Nodes))
	}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, e := range g.Edges {
		if !known[e.From] || !known[e.To] {
			t.Fatalf("dangling edge %+v", e)
		}
	}
}

func TestFluxGraphResourceLimit(t *testing.T) {
	var entries []any
	for i := 0; i < graphMaxNodes+100; i++ {
		entries = append(entries, map[string]any{"id": fmt.Sprintf("ns_cm%d__ConfigMap", i), "v": "v1"})
	}
	k := fluxgKust("ns", "big")
	k.Object["status"] = map[string]any{"inventory": map[string]any{"entries": entries}}
	_ = unstructured.SetNestedField(k.Object, map[string]any{"kind": "GitRepository", "name": "r"}, "spec", "sourceRef")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), k, fluxgGitRepo("ns", "r", true))
	g, err := fluxgGraph(t, fluxgKube("ns"), dyn, FluxRef{FluxKindKustomization, "ns", "big"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Truncated || len(g.Nodes) > graphMaxNodes {
		t.Fatalf("truncated=%v nodes=%d (limit %d)", g.Truncated, len(g.Nodes), graphMaxNodes)
	}
	// Flux-вершины не вытесняются ресурсами.
	if fluxgNode(g, graphID("kustomize.toolkit.fluxcd.io", "Kustomization", "ns", "big")) == nil ||
		fluxgNode(g, graphID("source.toolkit.fluxcd.io", "GitRepository", "ns", "r")) == nil {
		t.Fatal("Flux nodes must stay when resources are truncated")
	}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, e := range g.Edges {
		if !known[e.From] || !known[e.To] {
			t.Fatalf("dangling edge %+v", e)
		}
	}
}

func TestFluxGraphMissingAndForbiddenSource(t *testing.T) {
	k := fluxgFixture(t, "kustomization-v1.yaml")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), k) // нет ни источника, ни зависимости
	g, err := fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "apps"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Warnings) != 2 {
		t.Fatalf("warnings: %v", g.Warnings)
	}
	// Запрещённый источник — предупреждение без текста ответа сервера.
	dyn.PrependReactor("get", "gitrepositories", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "gitrepositories"}, "fleet", errors.New("S3CRET-denied"))
	})
	g, err = fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "apps"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range g.Warnings {
		found = found || strings.Contains(w, "no access to GitRepository flux-system/fleet")
		if strings.Contains(w, "S3CRET") {
			t.Fatalf("server text leaked into warnings: %q", w)
		}
	}
	if !found {
		t.Fatalf("warnings: %v", g.Warnings)
	}
}

func TestFluxGraphForbiddenPodsIsWarning(t *testing.T) {
	kube := fluxgKube("prod")
	kube.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("no"))
	})
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fluxgFixture(t, "kustomization-v1.yaml"),
		fluxgGitRepo("flux-system", "fleet", true), fluxgKust("flux-system", "infra"))
	g, err := fluxgGraph(t, kube, dyn, FluxRef{FluxKindKustomization, "flux-system", "apps"})
	if err != nil || len(g.Warnings) == 0 {
		t.Fatalf("err=%v warnings=%v", err, g.Warnings)
	}
}

func TestFluxGraphRejectsBadRefs(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	for _, ref := range []FluxRef{
		{"", "ns", "a"}, {FluxKindKustomization, "", "a"}, {FluxKindKustomization, "ns", ""},
		{FluxKindKustomization, strings.Repeat("x", 201), "a"},
	} {
		if _, err := fluxgGraph(t, fluxgKube("ns"), dyn, ref); !errors.Is(err, errFluxInvalidRef) {
			t.Errorf("%+v: err=%v", ref, err)
		}
	}
	for _, kind := range []string{FluxKindGitRepository, FluxKindAlert, FluxKindImagePolicy, "Deployment"} {
		if _, err := fluxgGraph(t, fluxgKube("ns"), dyn, FluxRef{kind, "ns", "a"}); !errors.Is(err, errFluxKindUnsupported) {
			t.Errorf("%s: err=%v", kind, err)
		}
	}
	// Объекта нет — ошибка API как есть.
	if _, err := fluxgGraph(t, fluxgKube("ns"), dyn, FluxRef{FluxKindKustomization, "ns", "none"}); !apierrors.IsNotFound(err) {
		t.Errorf("not found: %v", err)
	}
	if _, err := NewManager().GetFluxGraph(FluxRef{FluxKindKustomization, "ns", "a"}); err == nil {
		t.Error("no active connection must be an error")
	}
}

func TestFluxGraphVersions(t *testing.T) {
	orig := fluxGraphVersions
	t.Cleanup(func() { fluxGraphVersions = orig })
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fluxgKust("ns", "a"))
	ref := FluxRef{FluxKindKustomization, "ns", "a"}
	fluxGraphVersions = func(*ClusterConnection) (map[string]string, error) { return map[string]string{}, nil }
	if _, err := fluxgGraph(t, fluxgKube("ns"), dyn, ref); !errors.Is(err, errFluxNotInstalled) {
		t.Errorf("no kind: %v", err)
	}
	fluxGraphVersions = func(*ClusterConnection) (map[string]string, error) {
		return map[string]string{FluxKindKustomization: "v1beta2"}, nil
	}
	if _, err := fluxgGraph(t, fluxgKube("ns"), dyn, ref); !errors.Is(err, errFluxVersionUnsupported) {
		t.Errorf("old version: %v", err)
	}
}

func TestFluxGraphOddObjectsDoNotPanic(t *testing.T) {
	for _, spec := range []map[string]any{
		{"sourceRef": "x", "dependsOn": "x"},
		{"sourceRef": map[string]any{"kind": int64(1)}, "dependsOn": []any{int64(1), "x", map[string]any{"name": int64(2)}, map[string]any{}}},
		{"kubeConfig": "x"}, {"kubeConfig": map[string]any{}}, {"dependsOn": []any{map[string]any{"name": ""}}},
	} {
		k := fluxObj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "ns", "a", spec)
		k.Object["status"] = map[string]any{"inventory": "x", "conditions": "x"}
		dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), k)
		if _, err := fluxgGraph(t, fluxgKube("ns"), dyn, FluxRef{FluxKindKustomization, "ns", "a"}); err != nil {
			t.Fatalf("spec %v: %v", spec, err)
		}
	}
}

// Содержимое объектов, которое Kubron не отдаёт наружу, в граф не попадает.
func TestFluxGraphNoSecretLeak(t *testing.T) {
	const marker = "S3CRET-graph"
	k := fluxgFixture(t, "kustomization-v1.yaml")
	_ = unstructured.SetNestedField(k.Object, map[string]any{"substitute": map[string]any{"PASSWORD": marker}}, "spec", "postBuild")
	k.SetAnnotations(map[string]string{fluxLastAppliedAnnotation: marker})
	src := fluxgGitRepo("flux-system", "fleet", true)
	_ = unstructured.SetNestedField(src.Object, "https://user:"+marker+"@host/repo.git", "spec", "url")
	_ = unstructured.SetNestedField(src.Object, map[string]any{"type": "Ready", "status": "False", "message": "token=" + marker}, "status", "artifactNote")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), k, src, fluxgKust("flux-system", "infra"))
	g, err := fluxgGraph(t, fluxgKube("prod"), dyn, FluxRef{FluxKindKustomization, "flux-system", "apps"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), marker) {
		t.Fatalf("object content leaked into the graph: %s", raw)
	}
}
