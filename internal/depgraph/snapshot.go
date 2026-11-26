// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

// Snapshot — сериализуемый снимок графа для отчёта и хранилища (ADR-0031 §7). Только идентификаторы ресурсов,
// состояния и коды причин: меток, аннотаций, хостов Ingress, значений полей и данных ConfigMap здесь нет.
type Snapshot struct {
	Nodes     []SnapshotNode `json:"nodes"`
	Edges     []SnapshotEdge `json:"edges"`
	Truncated bool           `json:"truncated,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

// SnapshotNode — вершина снимка.
type SnapshotNode struct {
	Group       string `json:"group,omitempty"`
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	Health      string `json:"health"`
	Reason      string `json:"reason,omitempty"`
	Unknown     string `json:"unknown,omitempty"`
	Placeholder bool   `json:"placeholder,omitempty"`
}

// SnapshotEdge — ребро снимка; From и To — Ref.Key().
type SnapshotEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Type   string `json:"type"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// Snapshot строит детерминированный снимок: вершины и рёбра отсортированы, срезы никогда не nil.
func (g *Graph) Snapshot() Snapshot {
	nodes := g.Nodes()
	edges := g.Edges()
	s := Snapshot{
		Nodes:     make([]SnapshotNode, 0, len(nodes)),
		Edges:     make([]SnapshotEdge, 0, len(edges)),
		Truncated: g.truncated,
		Warnings:  g.Warnings(),
	}
	if len(s.Warnings) == 0 {
		s.Warnings = nil
	}
	for _, n := range nodes {
		s.Nodes = append(s.Nodes, SnapshotNode{
			Group: n.Group, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name,
			Health: string(n.Health), Reason: n.Reason, Unknown: string(n.Unknown), Placeholder: n.Placeholder,
		})
	}
	for _, e := range edges {
		s.Edges = append(s.Edges, SnapshotEdge{
			From: e.From.Key(), To: e.To.Key(), Type: string(e.Type), State: string(e.State), Reason: e.Reason,
		})
	}
	return s
}
