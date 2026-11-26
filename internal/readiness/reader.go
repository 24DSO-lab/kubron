// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
)

// Reader — потребительский интерфейс чтения кластера: подмножество opsplatform.Reader, которое нужно проверке.
// Пакет не импортирует платформу; слой связки оборачивает Op.Reader в эти методы и приводит ошибки платформы к
// ErrSource* (errors.Is). Только чтение; Secret чтению не подлежит.
type Reader interface {
	ServerVersion(ctx context.Context) (version.Info, error)
	ServerResources(ctx context.Context) ([]*metav1.APIResourceList, error)
	List(ctx context.Context, gvk schema.GroupVersionKind, opts ListOptions) (*ListResult, error)
}

// ListOptions — параметры выборки (подмножество opsplatform.ListOptions).
type ListOptions struct {
	Namespace string
	Limit     int64
}

// ListResult — результат выборки. Truncated — объектов больше, чем вернулось: просмотр неполный.
type ListResult struct {
	Items      []unstructured.Unstructured
	Truncated  bool
	ObservedAt time.Time
}

// PartialError — источник прочитан частично: вместе с ошибкой возвращены прочитанные данные. Is(ErrSourcePartial).
type PartialError struct{ Failed []string }

func (e *PartialError) Error() string {
	return fmt.Sprintf("%v: %s", ErrSourcePartial, strings.Join(e.Failed, ", "))
}

// Is связывает PartialError с ErrSourcePartial.
func (e *PartialError) Is(target error) bool { return target == ErrSourcePartial }

// servedKinds возвращает виды, обслуживаемые кластером: «группа/версия» → набор Kind. Подресурсы (pods/log) пропускаются.
func servedKinds(lists []*metav1.APIResourceList) map[schema.GroupVersion]map[string]bool {
	out := map[schema.GroupVersion]map[string]bool{}
	for _, l := range lists {
		if l == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") || r.Kind == "" {
				continue
			}
			if out[gv] == nil {
				out[gv] = map[string]bool{}
			}
			out[gv][r.Kind] = true
		}
	}
	return out
}

// versionsOf возвращает версии вида «группа/Kind», которые обслуживает кластер, по возрастанию.
func versionsOf(served map[schema.GroupVersion]map[string]bool, gk schema.GroupKind) []string {
	var out []string
	for gv, kinds := range served {
		if gv.Group == gk.Group && kinds[gk.Kind] {
			out = append(out, gv.Version)
		}
	}
	slices.Sort(out)
	return out
}
