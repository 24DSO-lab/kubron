// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	// ВРЕМЕННО: заглушка контракта D2. После слияния скелета D2 заменить на internal/depgraph (см. stubs/depgraph).
	"github.com/24DSO-lab/kubron/internal/diagnose/stubs/depgraph"
)

// Типы и ошибки чтения живут в depgraph (ADR-0031 §4): граф и диагностика читают кластер одним и тем же
// интерфейсом, поэтому здесь — псевдонимы, а не копии. Тексты ошибок совпадают с ошибками платформенного Reader
// (их шаблоны уже есть в backend.json): адаптер слоя связки оборачивает ошибку платформы так, что errors.Is видит
// и ту, и другую. Идентичность значений ошибок сохраняется (это одни и те же переменные).
type (
	// ListOptions — параметры выборки (подмножество платформенных).
	ListOptions = depgraph.ListOptions
	// ListResult — результат List. Truncated=true — объектов больше, чем вернулось: отчёт обязан показать неполноту.
	ListResult = depgraph.ListResult
)

var (
	ErrNotFound    = depgraph.ErrNotFound
	ErrForbidden   = depgraph.ErrForbidden
	ErrUnavailable = depgraph.ErrUnavailable
	ErrTooLarge    = depgraph.ErrTooLarge
)

// LogRequest — запрос лога контейнера.
type LogRequest struct {
	Namespace, Pod, Container string
	Previous                  bool
	TailLines, LimitBytes     int64
}

// Reader — read-only чтение кластера, нужное диагностике: потребительский интерфейс (подмножество платформенного
// Reader, ADR-0026 §1). Разрешены только get/list для pods, pods/log, events, replicasets, deployments, namespaces
// и nodes; Secret не читается и не входит в allowlist. Реализация сама ограничивает deadline, размер и параллелизм.
type Reader interface {
	Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	List(ctx context.Context, gvk schema.GroupVersionKind, opts ListOptions) (*ListResult, error)
	PodLogs(ctx context.Context, req LogRequest) ([]byte, error)
}

// Виды ресурсов allowlist диагностики. Любое другое чтение — ошибка проектирования; тест AllowedKinds следит.
var (
	gvkPod        = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	gvkNamespace  = schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}
	gvkNode       = schema.GroupVersionKind{Version: "v1", Kind: "Node"}
	gvkEvent      = schema.GroupVersionKind{Version: "v1", Kind: "Event"}
	gvkReplicaSet = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	gvkDeployment = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
)

// AllowedKinds — виды, которые читает диагностика (для документации и тестов, сверяемых с allowlist платформы).
func AllowedKinds() []schema.GroupKind {
	return []schema.GroupKind{
		gvkPod.GroupKind(), gvkNamespace.GroupKind(), gvkNode.GroupKind(),
		gvkEvent.GroupKind(), gvkReplicaSet.GroupKind(), gvkDeployment.GroupKind(),
	}
}
