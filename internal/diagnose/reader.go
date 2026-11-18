// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
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
	gvkService    = schema.GroupVersionKind{Version: "v1", Kind: "Service"}
	gvkPVC        = schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolumeClaim"}
	gvkReplicaSet = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	gvkDeployment = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
)

// AllowedKinds — виды, которые читает диагностика, включая граф зависимостей (для документации и тестов, сверяемых
// с allowlist платформы). Состав: ADR-0024 §2 (шесть видов), этап 1 ADR-0031 §6 (Service, EndpointSlice, PVC, PV,
// StorageClass) и владельцы Pod, которых граф читает по ownerReferences (StatefulSet, DaemonSet, Job, CronJob).
// Secret здесь нет и быть не может; ConfigMap граф читает только при флаге этапа 2, выключенном по умолчанию.
func AllowedKinds() []schema.GroupKind {
	return []schema.GroupKind{
		gvkPod.GroupKind(), gvkNamespace.GroupKind(), gvkNode.GroupKind(),
		gvkEvent.GroupKind(), gvkReplicaSet.GroupKind(), gvkDeployment.GroupKind(),
		gvkService.GroupKind(), gvkPVC.GroupKind(),
		{Kind: "PersistentVolume"}, {Group: "storage.k8s.io", Kind: "StorageClass"},
		{Group: "discovery.k8s.io", Kind: "EndpointSlice"},
		{Group: "apps", Kind: "StatefulSet"}, {Group: "apps", Kind: "DaemonSet"},
		{Group: "batch", Kind: "Job"}, {Group: "batch", Kind: "CronJob"},
	}
}
