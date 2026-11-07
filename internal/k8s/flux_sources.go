// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: чтение источников реализует B.4.

// GetFluxSources возвращает источники (GitRepository, OCIRepository, HelmRepository, HelmChart, Bucket).
func (m *Manager) GetFluxSources(_ []string) ([]FluxSourceInfo, error) {
	return []FluxSourceInfo{}, nil
}
