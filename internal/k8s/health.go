// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
)

// Health возвращает доску состояний соединения кластеров (общую для всех видов).
func (m *Manager) Health() *clusterhealth.Board { return m.health }

// NewHealthMonitor создаёт фоновую проверку кластеров поверх общей доски. Запуск и настройка
// (интервал, хранилище, лидер) — на вызывающем; десктоп монитор не запускает: там состояние
// известно из самого подключения, а лишние фоновые обращения к кластерам пользователю не нужны.
func (m *Manager) NewHealthMonitor() *clusterhealth.Monitor {
	return &clusterhealth.Monitor{
		Board: m.health,
		List: func() ([]string, error) {
			list, err := ListClusters()
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(list))
			for i := range list {
				ids = append(ids, list[i].ID)
			}
			return ids, nil
		},
		Probe: m.probeCluster,
	}
}

// probeCluster проверяет один кластер: готовое подключение из пула или временное (оно не попадает
// в пул и не запускает informers). Проверка — запрос списка namespace с лимитом 1: он требует
// действительных учётных данных, а ответ «нет прав» значит лишь, что кластер доступен.
func (m *Manager) probeCluster(ctx context.Context, id string) clusterhealth.Health {
	conn, ok := m.pool.get(id)
	if !ok {
		var err error
		if conn, err = buildClusterConnection(id); err != nil {
			return clusterhealth.Health{State: clusterhealth.Misconfigured, Message: err.Error()}
		}
	}
	// Учётные данные от внешней команды (exec) в фоне не запускаем: это выполнение чужого кода без
	// участия пользователя, да ещё раз в полминуты.
	if conn.Config != nil && conn.Config.ExecProvider != nil {
		return clusterhealth.Health{State: clusterhealth.Unknown, Message: "credentials come from an external command; not checked in the background"}
	}
	start := time.Now()
	_, err := conn.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	state, msg := classifyProbeError(err)
	return clusterhealth.Health{State: state, Message: msg, LatencyMs: time.Since(start).Milliseconds()}
}

// classifyProbeError переводит ошибку проверки в состояние и причину.
func classifyProbeError(err error) (clusterhealth.State, string) {
	switch {
	case err == nil:
		return clusterhealth.OK, ""
	case apierrors.IsUnauthorized(err):
		return clusterhealth.Unauthorized, err.Error()
	case apierrors.IsForbidden(err):
		// Кластер ответил и принял учётные данные; у пользователя kubeconfig просто нет права смотреть namespace.
		return clusterhealth.OK, ""
	case errors.Is(err, context.DeadlineExceeded):
		return clusterhealth.Unreachable, "the cluster did not respond in time"
	default:
		return clusterhealth.Unreachable, err.Error()
	}
}
