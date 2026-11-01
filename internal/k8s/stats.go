// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// ActiveLogStreams — сколько потоков логов сейчас запущено у вида (для метрик).
func (m *Manager) ActiveLogStreams() int {
	if m == nil {
		return 0
	}
	m.logs.mu.Lock()
	defer m.logs.mu.Unlock()
	return len(m.logs.m)
}

// Len — сколько port-forward сейчас учтено у менеджера (для метрик).
func (pfm *PortForwardManager) Len() int {
	if pfm == nil {
		return 0
	}
	pfm.mu.Lock()
	defer pfm.mu.Unlock()
	return len(pfm.sessions)
}
