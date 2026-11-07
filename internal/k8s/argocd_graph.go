// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// argoGraphMaxNodes — предел размера графа: больше интерфейс всё равно не покажет читаемо (общий для всех графов).
	argoGraphMaxNodes = graphMaxNodes

	ArgoEdgeOwns    = graphEdgeOwns    // владелец → дочерний объект (Deployment → ReplicaSet → Pod)
	ArgoEdgeSelects = graphEdgeSelects // Service → Pod по селектору
	ArgoEdgeRoutes  = graphEdgeRoutes  // Ingress → Service
)

// ArgoGraphNode — вершина графа ресурсов приложения.
type ArgoGraphNode struct {
	ID        string `json:"id"` // group/kind/namespace/name
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"` // sync: Synced | OutOfSync | ""
	Health    string `json:"health"`
	Message   string `json:"message"`
	Hook      bool   `json:"hook"`
	// Managed: ресурс есть в status.resources приложения. false — найден по связям (ReplicaSet, Pod, Service Ingress'а).
	Managed bool `json:"managed"`
}

// ArgoGraphEdge — связь между вершинами.
type ArgoGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // owns | selects | routes
}

// ArgoGraph — граф ресурсов приложения. Warnings — то, что не удалось прочитать (например, нет прав на list pods):
// частичный граф лучше ошибки.
type ArgoGraph struct {
	Nodes     []ArgoGraphNode `json:"nodes"`
	Edges     []ArgoGraphEdge `json:"edges"`
	Truncated bool            `json:"truncated"` // достигнут предел argoGraphMaxNodes
	Warnings  []string        `json:"warnings"`
}

// GetArgoApplicationGraph строит граф ресурсов Application: status.resources плюс связи владения,
// Service → Pod и Ingress → Service. Читает только Kubernetes API.
func (m *Manager) GetArgoApplicationGraph(namespace, name string) (*ArgoGraph, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 30*time.Second)
	defer cancel()

	u, err := conn.DynamicClient.Resource(kindTable["application"].gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get application %s/%s: %w", namespace, name, err)
	}
	return buildArgoGraph(ctx, conn.Client, argoResources(u.Object)), nil
}

// buildArgoGraph превращает разобранные status.resources в Argo-граф: связи строит общий buildResourceGraph,
// здесь к вершинам из status.resources возвращаются поля, которых нет в нейтральной модели (sync-статус, сообщение, hook).
func buildArgoGraph(ctx context.Context, c kubernetes.Interface, refs []ArgoResourceRef) *ArgoGraph {
	graphRefs := make([]graphRef, len(refs))
	byID := make(map[string]ArgoResourceRef, len(refs))
	for i, r := range refs {
		graphRefs[i] = graphRef{Group: r.Group, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, Health: r.Health}
		id := graphID(r.Group, r.Kind, r.Namespace, r.Name)
		if _, dup := byID[id]; !dup { // повтор игнорируется: первая запись выигрывает, как и в общем построителе
			byID[id] = r
		}
	}

	rg := buildResourceGraph(ctx, c, graphRefs)

	g := &ArgoGraph{
		Nodes:     make([]ArgoGraphNode, len(rg.Nodes)),
		Edges:     make([]ArgoGraphEdge, len(rg.Edges)),
		Truncated: rg.Truncated,
		Warnings:  rg.Warnings,
	}
	for i, n := range rg.Nodes {
		an := ArgoGraphNode{
			ID: n.ID, Group: n.Group, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name,
			Health: n.Health, Managed: n.Managed,
		}
		if n.Managed {
			r := byID[n.ID]
			an.Status, an.Message, an.Hook = r.Status, r.Message, r.Hook
		}
		g.Nodes[i] = an
	}
	for i, e := range rg.Edges {
		g.Edges[i] = ArgoGraphEdge{From: e.From, To: e.To, Type: e.Type}
	}
	return g
}
