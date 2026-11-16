// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "context"

// Limits — верхние границы построения. В коде, не в настройках; меняются только новым ADR (ADR-0031 §3).
type Limits struct{ MaxNodes, MaxEdges, MaxDepth, MaxReads int }

// DefaultLimits — стартовые значения ADR-0031 §3: 200 вершин, 600 рёбер, глубина 4, 40 запросов чтения.
var DefaultLimits = Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}

// Options — параметры построения. Этап 1: все флаги расширений выключены (ADR-0031 §6).
type Options struct {
	Limits              Limits
	ExpandIngress       bool
	ExpandHPA           bool
	ExpandConfigMapRefs bool
}

// Build строит граф от цели seed. Ошибка — только программная; недоступность, запрет и пределы дают Unknown,
// Truncated и Warnings. Заглушка скелета: граф из одной вершины-цели.
func Build(_ context.Context, _ Reader, seed Ref, o Options) (*Graph, error) {
	if o.Limits == (Limits{}) {
		o.Limits = DefaultLimits
	}
	g := New(seed)
	g.AddNode(Node{Ref: seed, Health: Unknown, Unknown: UnknownLimit})
	return g, nil
}
