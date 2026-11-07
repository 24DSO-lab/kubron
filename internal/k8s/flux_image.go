// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: чтение Image automation реализует B.6.

// GetFluxImageAutomation возвращает ImageRepository, ImagePolicy и ImageUpdateAutomation.
func (m *Manager) GetFluxImageAutomation(_ []string) (*FluxImageAutomation, error) {
	return &FluxImageAutomation{
		Repositories: []FluxImageRepositoryInfo{},
		Policies:     []FluxImagePolicyInfo{},
		Automations:  []FluxImageUpdateAutomationInfo{},
	}, nil
}
