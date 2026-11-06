// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (m *Manager) QueryPrometheus(query string) (*PromQueryResult, error) {
	return m.promQuery("/api/v1/query", map[string]string{
		"query": query,
	})
}

func (m *Manager) QueryPrometheusRange(query string, start, end time.Time, step string) (*PromQueryResult, error) {
	return m.promQuery("/api/v1/query_range", map[string]string{
		"query": query,
		"start": fmt.Sprintf("%d", start.Unix()),
		"end":   fmt.Sprintf("%d", end.Unix()),
		"step":  step,
	})
}

// prometheusServiceName — имя сервиса:порт Prometheus для API-прокси Kubernetes.
const prometheusServiceName = "prometheus:web"

// promQuery выполняет запрос к Prometheus через API-прокси сервиса. Аутентификация,
// TLS и прокси берутся из клиента client-go, отдельный HTTP-клиент не нужен.
func (m *Manager) promQuery(path string, params map[string]string) (*PromQueryResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 30*time.Second)
	defer cancel()

	req := conn.Client.CoreV1().RESTClient().Get().
		Namespace(MetricsNamespace).
		Resource("services").
		Name(prometheusServiceName).
		SubResource("proxy").
		Suffix(strings.TrimPrefix(path, "/"))
	for k, v := range params {
		req = req.Param(k, v)
	}

	body, err := req.DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	var result PromQueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &result, nil
}
