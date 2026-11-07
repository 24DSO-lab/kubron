// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: обнаружение Flux реализует B.1 (flux_status.go, flux_status_test.go).

// GetFluxStatus сообщает, что Flux не установлен: раздел в меню скрыт.
func (m *Manager) GetFluxStatus() (*FluxStatus, error) {
	return &FluxStatus{Versions: map[string]string{}}, nil
}

// fluxServedVersions возвращает обслуживаемую версию API по виду («Kustomization» → «v1»).
// Заглушка отдаёт первую допустимую версию каждого вида; B.1 заменит её результатом discovery с коротким TTL.
func fluxServedVersions(_ *ClusterConnection) (map[string]string, error) {
	out := make(map[string]string, len(fluxKinds))
	for _, k := range fluxKinds {
		out[k.Kind] = k.Versions[0]
	}
	return out, nil
}
