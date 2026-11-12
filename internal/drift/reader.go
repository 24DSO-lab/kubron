// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

// Ошибки чтения кластера. Домен не импортирует платформу операционных модулей (ADR-0026): адаптер wire-слоя
// переводит ошибки платформы в эти значения (errors.Is проходит через обёртку %w). Любая другая ошибка Reader
// трактуется как «кластер недоступен».
var (
	// ErrNotFound — объекта нет. У List означает, что тип ресурса в кластере не существует: список объектов
	// несуществующего пространства имён возвращается пустым, а не ошибкой.
	ErrNotFound = errors.New("cluster resource not found")
	// ErrForbidden — у учётной записи кластера нет права на чтение или тип вне allowlist платформы.
	ErrForbidden = errors.New("cluster read is forbidden")
	// ErrUnavailable — ошибка API, таймаут, сеть.
	ErrUnavailable = errors.New("cluster is unavailable")
	// ErrKindUnavailable — тип ресурса в кластере не существует (нет CRD, версия API не поддерживается).
	ErrKindUnavailable = errors.New("cluster resource kind is unavailable")
	// ErrTooLarge — ответ превысил лимит размера платформы.
	ErrTooLarge = errors.New("cluster response is too large")
)

// ListResult — ответ List: объекты и признак того, что выборка обрезана лимитом.
type ListResult struct {
	Items     []unstructured.Unstructured
	Truncated bool
}

// ClusterReader — узкий интерфейс чтения кластера, нужный домену: подмножество opsplatform.Reader (только чтение).
// Объявлен здесь, у потребителя; реализует его адаптер wire-слоя поверх opsplatform.Reader, а в тестах — двойник.
// Реализация обязана быть read-only и уважать deadline контекста.
type ClusterReader interface {
	Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	List(ctx context.Context, gvk schema.GroupVersionKind, namespace, labelSelector string, limit int64) (ListResult, error)
}

// AuditLookup — поиск записи аудита Kubron о применении YAML к ресурсу (ADR-0027 §6). Реализуется в wire-слое по
// журналу аудита; без него источник «запись аудита Kubron» не устанавливается.
type AuditLookup interface {
	// LastApply возвращает время последней успешной записи «YAML применён» к ресурсу ref не раньше since. Имя
	// пользователя не возвращается: показ имён требует права на аудит, а проверка baseline — только права чтения.
	LastApply(ctx context.Context, ref baseline.Ref, since time.Time) (at time.Time, found bool, err error)
}

// listLimit — размер выборки List: не больше предела платформы на ответ.
const listLimit = 1000

// Skip — то, что не удалось снять в версию: ресурс (Name заполнен) или тип в пространстве имён (Name пуст).
type Skip struct {
	Ref    baseline.Ref    `json:"ref"`
	Reason baseline.Reason `json:"reason"`
}
