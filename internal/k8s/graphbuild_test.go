// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// graphScenario — кластер и ссылки для проверки графа: владение, Service → Pod, Ingress → Service.
func graphScenario() (*fake.Clientset, []ArgoResourceRef) {
	const ns = "prod"
	c := fake.NewSimpleClientset(
		&appsv1.ReplicaSet{ObjectMeta: meta(ns, "web-abc", nil, ctrlOwner("apps/v1", "Deployment", "web"))},
		&corev1.Pod{
			ObjectMeta: meta(ns, "web-abc-1", map[string]string{"app": "web"}, ctrlOwner("apps/v1", "ReplicaSet", "web-abc")),
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
		},
		&corev1.Service{ObjectMeta: meta(ns, "web", nil, nil), Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "web"}}},
		&networkingv1.Ingress{ObjectMeta: meta(ns, "web", nil, nil), Spec: networkingv1.IngressSpec{
			DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "web"}},
			Rules: []networkingv1.IngressRule{{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "shared-auth"}}}},
			}}}},
		}},
	)
	refs := []ArgoResourceRef{
		{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: ns, Name: "web", Status: "Synced", Health: "Healthy", Message: "ok"},
		{Version: "v1", Kind: "Service", Namespace: ns, Name: "web", Status: "OutOfSync", Health: "Progressing", Hook: true},
		{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Namespace: ns, Name: "web", Status: "Synced"},
		{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "reader", Status: "Synced", Health: "Healthy"},
		// Повтор того же ресурса: первая запись выигрывает, как и раньше.
		{Group: "apps", Kind: "Deployment", Namespace: ns, Name: "web", Status: "OutOfSync", Health: "Degraded"},
	}
	return c, refs
}

// argoGraphGolden — JSON Argo-графа, снятый до выноса построителя в общую функцию.
// Совпадение байт в байт гарантирует, что контракт для UI не изменился.
const argoGraphGolden = `{"nodes":[` +
	`{"id":"/Pod/prod/web-abc-1","group":"","kind":"Pod","namespace":"prod","name":"web-abc-1","status":"","health":"Healthy","message":"","hook":false,"managed":false},` +
	`{"id":"/Service/prod/shared-auth","group":"","kind":"Service","namespace":"prod","name":"shared-auth","status":"","health":"","message":"","hook":false,"managed":false},` +
	`{"id":"/Service/prod/web","group":"","kind":"Service","namespace":"prod","name":"web","status":"OutOfSync","health":"Progressing","message":"","hook":true,"managed":true},` +
	`{"id":"apps/Deployment/prod/web","group":"apps","kind":"Deployment","namespace":"prod","name":"web","status":"Synced","health":"Healthy","message":"ok","hook":false,"managed":true},` +
	`{"id":"apps/ReplicaSet/prod/web-abc","group":"apps","kind":"ReplicaSet","namespace":"prod","name":"web-abc","status":"","health":"","message":"","hook":false,"managed":false},` +
	`{"id":"networking.k8s.io/Ingress/prod/web","group":"networking.k8s.io","kind":"Ingress","namespace":"prod","name":"web","status":"Synced","health":"","message":"","hook":false,"managed":true},` +
	`{"id":"rbac.authorization.k8s.io/ClusterRole//reader","group":"rbac.authorization.k8s.io","kind":"ClusterRole","namespace":"","name":"reader","status":"Synced","health":"Healthy","message":"","hook":false,"managed":true}` +
	`],"edges":[` +
	`{"from":"/Service/prod/web","to":"/Pod/prod/web-abc-1","type":"selects"},` +
	`{"from":"apps/Deployment/prod/web","to":"apps/ReplicaSet/prod/web-abc","type":"owns"},` +
	`{"from":"apps/ReplicaSet/prod/web-abc","to":"/Pod/prod/web-abc-1","type":"owns"},` +
	`{"from":"networking.k8s.io/Ingress/prod/web","to":"/Service/prod/shared-auth","type":"routes"},` +
	`{"from":"networking.k8s.io/Ingress/prod/web","to":"/Service/prod/web","type":"routes"}` +
	`],"truncated":false,"warnings":[]}`

// TestArgoGraphJSONUnchanged: рефакторинг построителя не меняет ни одного байта ответа Argo-графа.
func TestArgoGraphJSONUnchanged(t *testing.T) {
	c, refs := graphScenario()
	raw, err := json.Marshal(buildArgoGraph(context.Background(), c, refs))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != argoGraphGolden {
		t.Fatalf("Argo graph JSON changed.\n got: %s\nwant: %s", got, argoGraphGolden)
	}
}

// Общий построитель не зависит от Argo: на входе нейтральные ссылки, на выходе нейтральные вершины и рёбра.
func TestBuildResourceGraphNeutralTypes(t *testing.T) {
	c, _ := graphScenario()
	refs := []graphRef{
		{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web", Health: "Healthy"},
		{Version: "v1", Kind: "Service", Namespace: "prod", Name: "web"},
		{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web", Health: "Degraded"}, // повтор
	}
	g := buildResourceGraph(context.Background(), c, refs)

	byID := map[string]graphNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	dep := byID[graphID("apps", "Deployment", "prod", "web")]
	if !dep.Managed || dep.Version != "v1" || dep.Health != "Healthy" {
		t.Errorf("a node from refs keeps version and health, first record wins: %+v", dep)
	}
	rs := byID[graphID("apps", "ReplicaSet", "prod", "web-abc")]
	if rs.Managed || rs.Version != "" {
		t.Errorf("a node found by relations is not managed and has no version: %+v", rs)
	}
	pod := byID[graphID("", "Pod", "prod", "web-abc-1")]
	if pod.Managed || pod.Health != "Healthy" {
		t.Errorf("a pod found by relations gets health from its state: %+v", pod)
	}
	want := []graphEdge{
		{graphID("apps", "Deployment", "prod", "web"), graphID("apps", "ReplicaSet", "prod", "web-abc"), graphEdgeOwns},
		{graphID("apps", "ReplicaSet", "prod", "web-abc"), graphID("", "Pod", "prod", "web-abc-1"), graphEdgeOwns},
		{graphID("", "Service", "prod", "web"), graphID("", "Pod", "prod", "web-abc-1"), graphEdgeSelects},
	}
	have := map[graphEdge]bool{}
	for _, e := range g.Edges {
		have[e] = true
	}
	for _, e := range want {
		if !have[e] {
			t.Errorf("missing edge %+v", e)
		}
	}
}

func TestBuildResourceGraphEmptyIsNotNil(t *testing.T) {
	g := buildResourceGraph(context.Background(), fake.NewSimpleClientset(), nil)
	if g.Nodes == nil || g.Edges == nil || g.Warnings == nil || len(g.Nodes) != 0 || g.Truncated {
		t.Errorf("an empty graph must hold empty non-nil slices: %+v", g)
	}
}

// Предел размера действует и на входные ссылки (не только на найденные по связям вершины).
func TestBuildResourceGraphTruncatesRefs(t *testing.T) {
	refs := make([]graphRef, 0, graphMaxNodes+20)
	for i := range graphMaxNodes + 20 {
		refs = append(refs, graphRef{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: fmt.Sprintf("d-%04d", i)})
	}
	g := buildResourceGraph(context.Background(), fake.NewSimpleClientset(), refs)
	if !g.Truncated || len(g.Nodes) != graphMaxNodes {
		t.Fatalf("truncated=%v nodes=%d, expected limit %d", g.Truncated, len(g.Nodes), graphMaxNodes)
	}
}
