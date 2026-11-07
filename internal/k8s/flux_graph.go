// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: граф и цепочку реализует B.10.

// GetFluxGraph строит граф цепочки «источник → объект → ресурсы».
func (m *Manager) GetFluxGraph(_ FluxRef) (*FluxGraph, error) {
	return &FluxGraph{Nodes: []FluxGraphNode{}, Edges: []FluxGraphEdge{}, Warnings: []string{}}, nil
}
