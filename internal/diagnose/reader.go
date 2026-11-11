// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"time"
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

// ListResult — результат List. Truncated=true — объектов больше, чем вернулось: отчёт обязан показать неполноту.
type ListResult struct {
	Items      []unstructured.Unstructured
	Truncated  bool
	ObservedAt time.Time
}

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
