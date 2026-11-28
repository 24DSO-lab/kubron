// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"context"
	"time"
)

// ListOptions — постраничный список групп. After — ID последней группы предыдущей страницы (исключительно).
type ListOptions struct {
	Limit              int
	After              string
	IncludeDeactivated bool // tombstone'ы по умолчанию скрыты
}

// GroupPage — страница списка, упорядоченная по ID. NextAfter пуст на последней странице.
type GroupPage struct {
	Groups    []Group
	NextAfter string
}

// Backend — узкий контракт хранилища агрегата и snapshot'ов (ADR-0054 п.9). Реализации: File (Desktop, один
// процесс: сериализация read-modify-write + durable atomic replacement) и PostgreSQL (транзакция + условный
// UPDATE по revision) — их пишет Lane C. Обе обязаны проходить targettest.Run.
//
// Backend НЕ авторизует actor'а и НЕ вычисляет ownership: это Service и порты. Он обеспечивает структурные
// инварианты, неизменяемость owner'а, CAS и атомарность. Общий CRUD и last-write-wins запрещены.
//
// Правила для всех методов:
//   - Переходы состояния выполняются ТОЛЬКО чистыми функциями пакета (Group.Created, ApplyUpdate,
//     ApplyDeactivate, CheckSnapshotPublishable) внутри атомарной операции: так правила идентичны во всех backend'ах.
//   - Возвращаемые значения не разделяют память с хранилищем (Clone); snapshot неизменяем.
//   - Get/List/GetSnapshot возвращают только то, что видно Viewer'у; невидимое неотличимо от отсутствующего
//     (ErrNotFound) — наличие чужой группы не раскрывается.
//   - Любая ошибка хранилища, после которой исход нельзя доказать, — ErrUnavailable (fail closed). Отменённый
//     ctx до начала записи — тоже ErrUnavailable; состояние не меняется.
//   - При ошибке результат — нулевое значение.
type Backend interface {
	// CreateGroup создаёт группу: in.Revision=0, State=active; backend назначает Revision=1 (Group.Created).
	// Занятый ID — ErrAlreadyExists.
	CreateGroup(ctx context.Context, in Group) (Group, error)
	// GetGroup возвращает группу в любом состоянии (deactivated — данные, а не ошибка), если она видна viewer'у.
	GetGroup(ctx context.Context, viewer Viewer, id string) (Group, error)
	// UpdateGroup атомарно применяет Group.ApplyUpdate. Нет группы — ErrNotFound; stale — ErrStaleRevision.
	UpdateGroup(ctx context.Context, req UpdateGroupRequest) (Group, error)
	// DeactivateGroup атомарно применяет Group.ApplyDeactivate (tombstone). Физического удаления нет.
	DeactivateGroup(ctx context.Context, req DeactivateGroupRequest) (Group, error)
	// ListGroups возвращает видимые viewer'у группы, упорядоченные по ID.
	ListGroups(ctx context.Context, viewer Viewer, opts ListOptions) (GroupPage, error)

	// PublishSnapshot атомарно проверяет Group.CheckSnapshotPublishable против ТЕКУЩЕЙ группы и сохраняет
	// snapshot в одной consistency boundary (File — под одной блокировкой, PostgreSQL — в одной транзакции).
	// Повторного чтения revision до/после записи недостаточно. Нет группы — ErrNotFound; revision группы
	// отличается от s.GroupRevision либо группа деактивирована — ErrGroupRevisionChanged; занятый ID —
	// ErrAlreadyExists. Ни один из отказов не оставляет частичной записи.
	PublishSnapshot(ctx context.Context, s TargetSnapshot) (TargetSnapshot, error)
	// GetSnapshot возвращает snapshot, если его owner scope входит в Viewer.
	GetSnapshot(ctx context.Context, viewer Viewer, id string) (TargetSnapshot, error)
}

// OwnershipResolver — порт доверенного ownership (ADR-0053). Получает ТОЛЬКО ClusterID; OrgID/TeamID клиента
// не принимаются. Реализует интегратор поверх существующего cluster catalog (k8s.ClusterStore.Get, Directory,
// pgstore.Store.OrgID); второй каталог кластеров не создаётся. Не оценивает grants/delegation/RBAC, не читает
// credentials и не строит Kubernetes-клиентов.
//
// Контракт ошибок: ErrNotFound — кластер неизвестен; любая другая ошибка (недоступность, конфликт каталога,
// неполный scope) — отказ. Результат обязан быть полным: ровно одна Organization и не более одной Team;
// пустой TeamID — Organization-owned кластер, а НЕ wildcard. В Desktop/File — Organization LocalOrgID без Team.
type OwnershipResolver interface {
	ResolveClusterOwner(ctx context.Context, clusterID string) (ClusterOwner, error)
}

// ObservedObject — объект, наблюдаемый ClusterReader'ом. UID может оказаться пустым (тогда цель не включается
// в snapshot: target_identity_incomplete). ResourceVersion хранится отдельно от identity.
type ObservedObject struct {
	Group           string
	Version         string
	Kind            string
	Namespace       string
	Name            string
	UID             string
	ResourceVersion string
}

// GetObjectRequest — чтение одного явного member'а от имени requester'а.
type GetObjectRequest struct {
	Requester Actor
	Ref       ObjectRef
}

// ListObjectsRequest — ограниченное чтение по декларативному запросу в ОДНОМ кластере. Limit — максимум
// объектов (≥1); при превышении reader обязан вернуть первые Limit и Truncated=true, а не неограниченный набор.
type ListObjectsRequest struct {
	Requester  Actor
	ClusterID  string
	Group      string
	Version    string
	Kind       string
	Namespaces []string
	Labels     LabelSelector
	Fields     []FieldRequirement
	Limit      int
}

// ListObjectsResult — результат ограниченного чтения.
type ListObjectsResult struct {
	Objects   []ObservedObject
	Truncated bool
}

// ClusterReader — узкий read-only порт Kubernetes (ADR-0055). Домен НЕ создаёт Kubernetes-клиентов, НЕ читает
// kubeconfig и НЕ строит rest.Config: Lane C реализует порт поверх существующих Broker/PlanFor/ConfigFor
// (subject-aware credential path). Только чтение (get/list); мутаций нет.
//
// Контракт ошибок: ErrNotFound — объект отсутствует; ErrForbidden — requester не вправе читать (Kubernetes RBAC,
// credentials, tenant); ErrUnavailable либо любая иная ошибка — кластер недоступен. Reader не должен
// раскрывать, существует ли объект, который requester читать не вправе.
type ClusterReader interface {
	GetObject(ctx context.Context, req GetObjectRequest) (ObservedObject, error)
	ListObjects(ctx context.Context, req ListObjectsRequest) (ListObjectsResult, error)
}

// AdminAction — административное действие над группой.
type AdminAction string

const (
	AdminGroupCreate     AdminAction = "target-group-create"
	AdminGroupUpdate     AdminAction = "target-group-update"
	AdminGroupDeactivate AdminAction = "target-group-deactivate"
)

// Access — доверенные решения о полномочиях actor'а поверх app-RBAC (реализует интегратор). Роль и owner из UI
// не принимаются.
//
// AuthorizeGroupAdmin: nil — разрешено; ErrForbidden — запрещено; иная ошибка — зависимость недоступна (fail
// closed). Для create реализация ОБЯЗАНА проверить, что owner scope реален и доступен actor'у (Team существует
// в Directory и принадлежит Organization; actor вправе создавать группы в этом scope), и возвращать
// ErrForbidden одинаково для неизвестного и чужого scope. Sharing/видимость права изменять не дают.
//
// ViewerFor возвращает scope'ы, группы которых видны actor'у (членство в Organization/Team).
type Access interface {
	AuthorizeGroupAdmin(ctx context.Context, actor Actor, owner OwnerScope, action AdminAction) error
	ViewerFor(ctx context.Context, actor Actor) (Viewer, error)
}

// Clock — инъектируемые часы.
type Clock interface {
	Now() time.Time
}

// ClockFunc адаптирует функцию к Clock.
type ClockFunc func() time.Time

// Now реализует Clock.
func (f ClockFunc) Now() time.Time { return f() }

// IDGenerator выдаёт непрозрачные ID (группы, snapshot'ы). Результат обязан проходить validID.
type IDGenerator interface {
	NewID(prefix string) (string, error)
}
