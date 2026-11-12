// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/logredact"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
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
