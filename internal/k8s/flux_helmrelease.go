// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: чтение HelmRelease реализует B.3.

// GetFluxHelmReleases возвращает HelmRelease из набора namespace (пусто — из всех).
func (m *Manager) GetFluxHelmReleases(_ []string) ([]FluxHelmReleaseInfo, error) {
	return []FluxHelmReleaseInfo{}, nil
}
