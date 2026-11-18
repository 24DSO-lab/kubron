// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/logredact"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
	"github.com/24DSO-lab/kubron/internal/signals"
)

// Адаптеры между доменом Intelligence и платформой (ADR-0026): домены платформу не импортируют, переводит слой связки.

// mappedErr сохраняет текст ошибки платформы и добавляет доменную ошибку для errors.Is.
type mappedErr struct{ to, cause error }

func (m mappedErr) Error() string   { return m.cause.Error() }
func (m mappedErr) Unwrap() []error { return []error{m.cause, m.to} }

func mapReadErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, opsplatform.ErrNotFound):
		return mappedErr{diagnose.ErrNotFound, err}
	case errors.Is(err, opsplatform.ErrForbidden):
		return mappedErr{diagnose.ErrForbidden, err}
	case errors.Is(err, opsplatform.ErrTooLarge):
		return mappedErr{diagnose.ErrTooLarge, err}
	case errors.Is(err, opsplatform.ErrUnavailable):
		return mappedErr{diagnose.ErrUnavailable, err}
	}
	return err
}

// diagReader — diagnose.Reader поверх платформенного Reader (read-only, allowlist, лимитер — на стороне платформы).
type diagReader struct{ r opsplatform.Reader }

func (d diagReader) Get(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	u, err := d.r.Get(ctx, gvk, ns, name)
	return u, mapReadErr(err)
}

func (d diagReader) List(ctx context.Context, gvk schema.GroupVersionKind, o diagnose.ListOptions) (*diagnose.ListResult, error) {
	res, err := d.r.List(ctx, gvk, opsplatform.ListOptions{Namespace: o.Namespace, LabelSelector: o.LabelSelector,
		FieldSelector: o.FieldSelector, Limit: o.Limit})
	if err != nil {
		return nil, mapReadErr(err)
	}
	return &diagnose.ListResult{Items: res.Items, Truncated: res.Truncated, ObservedAt: res.ObservedAt}, nil
}

func (d diagReader) PodLogs(ctx context.Context, req diagnose.LogRequest) ([]byte, error) {
	b, err := d.r.PodLogs(ctx, opsplatform.LogRequest{Namespace: req.Namespace, Pod: req.Pod, Container: req.Container,
		Previous: req.Previous, TailLines: req.TailLines, LimitBytes: req.LimitBytes})
	return b, mapReadErr(err)
}

// mapMetricsErr приводит ошибку платформенного читателя метрик к ошибкам signals.Querier. Текст исходной ошибки
// сохраняется (платформа уже очистила его от выражений), остальные ошибки Provider считает недоступностью источника.
func mapMetricsErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, opsplatform.ErrNotFound):
		return mappedErr{signals.ErrNotFound, err}
	case errors.Is(err, opsplatform.ErrForbidden):
		return mappedErr{signals.ErrForbidden, err}
	case errors.Is(err, opsplatform.ErrTooLarge):
		return mappedErr{signals.ErrTooLarge, err}
	case errors.Is(err, opsplatform.ErrUnavailable):
		return mappedErr{signals.ErrUnavailable, err}
	}
	return err
}

// metricsQuerier — signals.Querier поверх платформенного MetricsReader (ADR-0032 §3): ни прав, ни пределов запроса
// он не добавляет и не меняет, это делает платформа.
type metricsQuerier struct{ r opsplatform.MetricsReader }

func (m metricsQuerier) Query(ctx context.Context, expr string, at time.Time) ([]signals.Sample, error) {
	res, err := m.r.Query(ctx, expr, at)
	if err != nil {
		return nil, mapMetricsErr(err)
	}
	out := make([]signals.Sample, len(res))
	for i, s := range res {
		out[i] = signals.Sample{Labels: s.Labels, Value: s.Value, At: s.At}
	}
	return out, nil
}

// metricSignals возвращает провайдера сигналов поверх читателя метрик операции. Метрик нет (стек не настроен,
// операция без подключения к кластеру) — nil: расследование идёт без сигналов, а не падает (ADR-0032 §7).
func metricSignals(m opsplatform.MetricsReader) signals.Provider {
	if m == nil {
		return nil
	}
	return signals.NewPrometheus(metricsQuerier{m})
}

// incidentRedactor — incident.Redactor поверх logredact (ADR-0022): fail-closed, ошибки переведены в доменные.
func incidentRedactor() incident.Redactor {
	return incident.RedactorFunc(func(in []byte) (incident.RedactResult, error) {
		res, err := logredact.Redact(in)
		switch {
		case errors.Is(err, logredact.ErrBinary):
			return incident.RedactResult{}, incident.ErrRedactNotText
		case errors.Is(err, logredact.ErrTooLarge):
			return incident.RedactResult{}, incident.ErrRedactTooLarge
		case err != nil:
			return incident.RedactResult{}, err
		}
		return incident.RedactResult{Text: res.Text, Replacements: res.Replacements, Version: res.Version}, nil
	})
}
