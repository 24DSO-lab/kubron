// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: удаление реализует B.9.

// DeleteFlux удаляет один Kustomization или HelmRelease.
func (m *Manager) DeleteFlux(_ FluxRef) error {
	return errFluxNotImplemented
}
