// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Метрики операционных модулей (ADR-0026 §6). Лейблы — только модуль, операция и статус: значения задаёт код,
// а не пользователь. Ни пользователей, ни кластеров, ни namespace в лейблах нет (ADR-0022); всё, что не похоже на
// идентификатор из кода, сворачивается в "other", чтобы кардинальность оставалась ограниченной.

// opLabel — допустимое значение лейбла module и operation.
var opLabel = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// opStatuses — фиксированный набор статусов операции.
var opStatuses = map[string]bool{"ok": true, "partial": true, "failed": true, "canceled": true}

func newOpsCollectors() (*prometheus.CounterVec, *prometheus.HistogramVec) {
	total := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "ops_operations_total",
		Help: "Operational module operations by module, operation and status.",
	}, []string{"module", "operation", "status"})
	dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "ops_operation_duration_seconds",
		Help:    "Operational module operation duration by module and operation.",
		Buckets: []float64{.01, .05, .25, 1, 2.5, 5, 15, 60},
	}, []string{"module", "operation"})
	return total, dur
}

func safeOpLabel(v string) string {
	if opLabel.MatchString(v) {
		return v
	}
	return "other"
}

// ObserveOp учитывает одну операцию операционного модуля (opsplatform.Observer).
func (m *Metrics) ObserveOp(module, operation, status string, d time.Duration) {
	if !opStatuses[status] {
		status = "other"
	}
	module, operation = safeOpLabel(module), safeOpLabel(operation)
	m.opsTotal.WithLabelValues(module, operation, status).Inc()
	m.opsDuration.WithLabelValues(module, operation).Observe(d.Seconds())
}
