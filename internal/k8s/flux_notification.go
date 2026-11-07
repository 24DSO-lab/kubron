// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: чтение Notification реализует B.5.

// GetFluxNotifications возвращает Provider, Alert и Receiver.
func (m *Manager) GetFluxNotifications(_ []string) (*FluxNotifications, error) {
	return &FluxNotifications{
		Providers: []FluxProviderInfo{},
		Alerts:    []FluxAlertInfo{},
		Receivers: []FluxReceiverInfo{},
	}, nil
}
