// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Ошибки чтения. Тексты совпадают с ошибками платформенного Reader (их шаблоны уже есть в backend.json):
// адаптер слоя связки оборачивает ошибку платформы так, что errors.Is видит и ту, и другую.
var (
	ErrNotFound    = errors.New("resource not found")
	ErrForbidden   = errors.New("access to the resource is forbidden")
	ErrUnavailable = errors.New("cluster source is unavailable")
	ErrTooLarge    = errors.New("response is too large")
)

// ListOptions — параметры выборки (подмножество платформенных).
type ListOptions struct {
	Namespace     string
	LabelSelector string
	FieldSelector string
	Limit         int64
}

// ListResult — результат List. Truncated=true — объектов больше, чем вернулось: граф обязан показать неполноту.
type ListResult struct {
	Items      []unstructured.Unstructured
	Truncated  bool
	ObservedAt time.Time
}

// Reader — read-only чтение кластера, нужное графу: потребительский интерфейс (подмножество платформенного
// Reader, ADR-0026 §1). Реализация сама ограничивает deadline, размер и параллелизм. Secret не читается.
type Reader interface {
	Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	List(ctx context.Context, gvk schema.GroupVersionKind, o ListOptions) (*ListResult, error)
}
