// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Заглушка задачи 0.2: reconcile, suspend и resume реализует B.8.

// ReconcileFlux запрашивает reconcile объектов (withSource — вместе с источником).
func (m *Manager) ReconcileFlux(_ []FluxRef, _ bool) ([]FluxActionResult, error) {
	return nil, errFluxNotImplemented
}

// SuspendFlux приостанавливает объекты (spec.suspend: true).
func (m *Manager) SuspendFlux(_ []FluxRef) ([]FluxActionResult, error) {
	return nil, errFluxNotImplemented
}

// ResumeFlux возобновляет объекты (spec.suspend: false и requestedAt).
func (m *Manager) ResumeFlux(_ []FluxRef) ([]FluxActionResult, error) {
	return nil, errFluxNotImplemented
}
