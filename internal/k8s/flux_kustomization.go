// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: чтение Kustomization реализует B.2.

// GetFluxKustomizations возвращает Kustomization из набора namespace (пусто — из всех).
func (m *Manager) GetFluxKustomizations(_ []string) ([]FluxKustomizationInfo, error) {
	return []FluxKustomizationInfo{}, nil
}
