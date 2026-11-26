// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// ConnectionFor возвращает подключение этого вида (сессии) к кластеру для операционных модулей (ADR-0026 §1).
// Кластер НЕ становится активным: активный кластер вида, informers и его удержание в пуле не меняются.
// Подключение строится так же, как при ConnectToCluster: PlanFor для пары «вид, кластер» и ConfigFor, поэтому
// действуют учётные данные именно этой сессии (личный вход, общий аккаунт команды), а не чужие.
func (m *Manager) ConnectionFor(clusterID string) (*ClusterConnection, error) {
	_, conn, err := m.connect(clusterID)
	return conn, err
}
