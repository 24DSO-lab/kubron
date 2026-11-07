// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
	"github.com/24DSO-labs/kubron/internal/credential"
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

// probeCluster проверяет один кластер. Общие учётные данные (team) — запросом списка namespace с лимитом 1: он
// требует действительных учётных данных, а ответ «нет прав» значит лишь, что кластер доступен. Личные (user) — только
// адрес и TLS: у фоновой проверки нет входа пользователя, и состояние «нужен вход» не должно выглядеть как
// «недоступен» (оно отдаётся в ClusterStatus.AuthRequired).
func (m *Manager) probeCluster(ctx context.Context, id string) clusterhealth.Health {
	sess := m.AuthSessionOf()
	sess.UserID = "" // у фоновой проверки нет пользователя
	if sess.Broker != nil {
		plan, err := PlanFor(sess, id)
		if err != nil {
			return clusterhealth.Health{State: clusterhealth.Misconfigured, Message: err.Error()}
		}
		m.noteScope(plan)
		if plan.Subject.Scope == credential.ScopeUser {
			plan.anonymous = true
			return probeAddress(ctx, plan)
		}
		return m.probePooled(ctx, plan.Key, func() (*ClusterConnection, error) { return buildPlanConnection(plan) })
	}
	return m.probePooled(ctx, id, func() (*ClusterConnection, error) { return buildClusterConnection(id) })
}

// probePooled проверяет готовое подключение из пула или временное (оно не попадает в пул и не запускает informers).
func (m *Manager) probePooled(ctx context.Context, key string, build func() (*ClusterConnection, error)) clusterhealth.Health {
	conn, ok := m.pool.get(key)
	if !ok {
		var err error
		if conn, err = build(); err != nil {
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

// probeAddress проверяет адрес и TLS кластера без учётных данных: любой HTTP-ответ (в том числе 401 и 403) значит, что
// адрес достижим и сертификат принят; ошибка соединения или TLS — «недоступен».
func probeAddress(ctx context.Context, plan ConnPlan) clusterhealth.Health {
	cfg, err := ConfigFor(plan)
	if err != nil {
		return clusterhealth.Health{State: clusterhealth.Misconfigured, Message: err.Error()}
	}
	cfg.Timeout = 10 * time.Second
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return clusterhealth.Health{State: clusterhealth.Misconfigured, Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/version", nil)
	if err != nil {
		return clusterhealth.Health{State: clusterhealth.Misconfigured, Message: err.Error()}
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		state, msg := classifyProbeError(err)
		if state == clusterhealth.OK { // сюда попадают только ошибки соединения: ответа не было
			state, msg = clusterhealth.Unreachable, err.Error()
		}
		return clusterhealth.Health{State: state, Message: msg, LatencyMs: time.Since(start).Milliseconds()}
	}
	_ = resp.Body.Close()
	return clusterhealth.Health{State: clusterhealth.OK, LatencyMs: time.Since(start).Milliseconds()}
}

// authRequired — нужен ли пользователю этого вида вход в кластер: у кластера личные учётные данные, а личного
// подключения нет или брокер сообщил, что вход пропал. Пока вид не знает, чьи данные у кластера, не утверждаем.
func (m *Manager) authRequired(clusterID string, connected bool) bool {
	if sc, ok := m.scopes.Load(clusterID); !ok || sc != credential.ScopeUser {
		return false
	}
	if !connected {
		return true
	}
	_, lost := m.authLost.Load(userPoolKey(clusterID, m.AuthSessionOf().UserID))
	return lost
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
