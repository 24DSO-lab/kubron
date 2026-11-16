// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"time"
)

// MetricSample — одна серия мгновенного вектора Prometheus: метки и одно значение. NaN и ±Inf передаются как есть:
// решает потребитель (каталог сигналов трактует их как отсутствие данных, а не как 0).
type MetricSample struct {
	Labels map[string]string
	Value  float64
	At     time.Time
}

// MetricsReader — безопасная передача мгновенного PromQL-запроса к Prometheus встроенного стека в контексте одной
// операции (ADR-0032 §3). Это отдельный интерфейс, а не методы Reader: контракт Reader заморожен, а добавление
// методов ломает его реализации и подставки. Выражение принимает только код каталога сигналов (internal/signals),
// интерфейсу пользователя произвольные запросы не доступны. Ошибки — ErrNotFound, ErrForbidden, ErrUnavailable,
// ErrTooLarge (errors.Is).
type MetricsReader interface {
	Query(ctx context.Context, expr string, at time.Time) ([]MetricSample, error)
}

// MetricsConfig — координаты стека мониторинга для API-прокси Kubernetes. Заполняет слой связки из единственного
// источника правды в internal/k8s (k8s.MetricsNamespace и k8s.PrometheusService); платформа их не дублирует.
type MetricsConfig struct {
	// Namespace — namespace стека (kubron-metrics).
	Namespace string
	// Service — имя «сервис:порт» для services/proxy (prometheus:web).
	Service string
}

// Configured сообщает, что координаты стека заполнены; иначе Op.Metrics остаётся nil.
func (c MetricsConfig) Configured() bool { return c.Namespace != "" && c.Service != "" }

// queryCounter — необязательная возможность MetricsReader: число выполненных запросов (реализация платформы её имеет).
type queryCounter interface{ Queries() int }

// MetricsQueries возвращает число запросов метрик, выполненных в операции, для Op.Audit (ADR-0032 §8): в аудит идёт
// только число, без выражений и значений. Метрик нет или читатель счётчика не ведёт — 0.
func (o *Op) MetricsQueries() int {
	if o == nil {
		return 0
	}
	if c, ok := o.Metrics.(queryCounter); ok {
		return c.Queries()
	}
	return 0
}
