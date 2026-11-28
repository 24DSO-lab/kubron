// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

import (
	"context"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// GrantBackend — узкий контракт хранилища grants (Contract Freeze §6.2). Реализации: File (этот пакет,
// Desktop, один процесс) и PostgreSQL-адаптер интегратора; обе обязаны проходить authorizationtest.Run.
//
// Backend проверяет структурные инварианты, неизменяемость полей, revision/CAS и переходы состояний. Он НЕ
// проверяет полномочия actor'а, НЕ пишет критичный аудит и НЕ вычисляет доступ — это Service и Evaluator.
//
// Общие правила:
//   - Входные значения каноникализуются (списки сортируются и дедуплицируются); возвращаемые значения не
//     разделяют память с хранилищем.
//   - Ссылка PrincipalRef сопоставляется целиком (Kind, ID, OrgID, TeamID): чужие записи не видны.
//   - expectedRevision == 0 не wildcard: для обновления/отзыва это ErrInvalid. Create требует отсутствия.
//   - Каждая успешная мутация увеличивает Revision ровно на 1 и сохраняет LastMutationID/LastAuditEventID.
//   - Идемпотентность: повтор мутации с тем же mutationID (LastMutationID записи), совпадающим содержимым и
//     Revision == expected+1 возвращает текущую запись без изменений; тот же mutationID с иным содержимым —
//     ErrConflict. Помнится только последняя мутация записи.
//   - Порядок проверок мутации: форма ввода → идемпотентный повтор → revision (ErrConflict) → допустимость
//     состояния (ErrRevoked/ErrInvalid).
//   - Любая ошибка хранилища, после которой исход записи нельзя доказать, — ErrUnavailable (fail closed).
//     Отменённый ctx до начала записи — тоже ErrUnavailable; состояние не меняется.
//
// Результат метода при ошибке — нулевое значение.
type GrantBackend interface {
	// CreateGrant создаёт active grant (in.Revision=0, in.State=active, LastMutationID/LastAuditEventID
	// заданы). Backend назначает Revision=1. Существующий ID — ErrConflict, кроме идемпотентного повтора.
	CreateGrant(ctx context.Context, in Grant) (Grant, error)
	// GetGrant возвращает grant в любом состоянии (revoked — данные, а не ошибка). Нет записи — ErrNotFound.
	GetGrant(ctx context.Context, id string) (Grant, error)
	// ListGrants возвращает все grants ровно этого principal (любых состояний), упорядоченные по ID.
	ListGrants(ctx context.Context, principal identity.PrincipalRef) ([]Grant, error)
	// UpdateGrant заменяет изменяемые поля active grant (Actions, Targets, Constraints, ExpiresAt) по CAS.
	// ID, Principal, CreatedBy и State неизменяемы (State остаётся active). in.Revision — 0 либо равна
	// expectedRevision; источник истины — аргумент. Отозванный grant — ErrRevoked.
	UpdateGrant(ctx context.Context, in Grant, expectedRevision uint64) (Grant, error)
	// RevokeGrant: active → revoked (CAS), терминально.
	RevokeGrant(ctx context.Context, id string, expectedRevision uint64, mutationID, auditEventID string) (Grant, error)
}

// DelegationBackend — контракт хранилища Cross-Team Delegation; общие правила — см. GrantBackend.
// Дополнительно: переходы active ↔ suspended выполняются через UpdateDelegation (in.State), отзыв —
// только через RevokeDelegation; revoked терминален (UpdateDelegation над ним — ErrRevoked).
type DelegationBackend interface {
	CreateDelegation(ctx context.Context, in Delegation) (Delegation, error)
	GetDelegation(ctx context.Context, id string) (Delegation, error)
	// ListDelegations возвращает delegations (любых состояний) данного ServicePrincipal от ровно этой Team.
	// Пустой ownerTeamID — ErrInvalid: «все команды» не запрашивается.
	ListDelegations(ctx context.Context, service identity.PrincipalRef, ownerTeamID string) ([]Delegation, error)
	// UpdateDelegation заменяет изменяемые поля и State (active|suspended) по CAS. ID, OrgID, OwnerTeamID,
	// ServicePrincipal и CreatedBy неизменяемы.
	UpdateDelegation(ctx context.Context, in Delegation, expectedRevision uint64) (Delegation, error)
	RevokeDelegation(ctx context.Context, id string, expectedRevision uint64, mutationID, auditEventID string) (Delegation, error)
}

// GrantReader и DelegationReader — read-подмножества backend'ов, которые нужны Evaluator'у.
type GrantReader interface {
	GetGrant(ctx context.Context, id string) (Grant, error)
	ListGrants(ctx context.Context, principal identity.PrincipalRef) ([]Grant, error)
}

type DelegationReader interface {
	GetDelegation(ctx context.Context, id string) (Delegation, error)
	ListDelegations(ctx context.Context, service identity.PrincipalRef, ownerTeamID string) ([]Delegation, error)
}

// TargetFacts — доверенные факты о цели из серверного каталога (ADR-0012). Не принимаются от клиента.
type TargetFacts struct {
	OrgID       string
	TeamID      string
	Environment string
}

// OwnershipResolver возвращает ТЕКУЩИЕ факты владения целью. Контракт ошибок: ErrNotFound — цель неизвестна;
// ErrUnavailable (или любая иная ошибка) — каталог недоступен/неоднозначен. Результат обязан указывать ровно
// одну Organization и одну Team; пустой/некорректный результат трактуется как отказ.
//
// Это порт authorization. Он отличен от identity.ClusterOwnerResolver: адаптеры могут читать один каталог,
// но контракты не взаимозаменяемы. Резолвер не оценивает grants, delegation, Kubernetes RBAC и credentials.
type OwnershipResolver interface {
	ResolveTargetFacts(ctx context.Context, target TargetRef) (TargetFacts, error)
}

// IdentityStateReader сообщает состояние технического principal'а. ВАЖНО: реализация обязана возвращать
// IdentityActive только для identity, чей критичный аудит последней мутации подтверждён (см.
// NewIdentityStateReader поверх identity.Service.Resolve). Неизвестный principal — ErrNotFound.
type IdentityStateReader interface {
	GetPrincipalState(ctx context.Context, principal identity.PrincipalRef) (identity.IdentityState, error)
}

// ActionFacts — доверенные факты версионированного каталога Operation/Action.
type ActionFacts struct {
	Destructive bool
}

// ActionFactsResolver возвращает факты действия. Неизвестная пара — ErrNotFound (grant на неизвестное
// действие ничего не разрешает); недоступность — ErrUnavailable.
type ActionFactsResolver interface {
	ResolveActionFacts(ctx context.Context, operationID, actionID string) (ActionFacts, error)
}

// Clock — инъектируемые часы (все решения об истечении/NotBefore принимаются по ним).
type Clock interface {
	Now() time.Time
}

// ClockFunc адаптирует функцию к Clock.
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

// OutcomeReader — read-подмножество audit.CriticalRecorder: подтверждение критичного аудита записи.
type OutcomeReader interface {
	GetOutcome(ctx context.Context, orgID, eventID string) (audit.CriticalOutcome, error)
}

// AdminAction — административное действие над grant/delegation (Contract Freeze §6.2).
type AdminAction string

const (
	AdminGrantCreate       AdminAction = "grant-create"
	AdminGrantUpdate       AdminAction = "grant-update"
	AdminGrantRevoke       AdminAction = "grant-revoke"
	AdminDelegationCreate  AdminAction = "delegation-create"
	AdminDelegationUpdate  AdminAction = "delegation-update"
	AdminDelegationSuspend AdminAction = "delegation-suspend"
	AdminDelegationRevoke  AdminAction = "delegation-revoke"
)

// AdminAuthorizer проверяет административные полномочия человека-actor'а поверх app-RBAC (реализует
// интегратор). Роль из UI не принимается. Возврат: nil — разрешено; ErrForbidden — запрещено; иная ошибка —
// зависимость недоступна (fail closed).
//
// AuthorizeGrantAdmin: управление grants ограничено владельцем identity principal'а. AuthorizeDelegationAdmin:
// Team Admin вправе управлять delegation ТОЛЬКО своей Team и её кластеров; Organization Admin выдавать или
// менять delegation от имени Team не вправе.
type AdminAuthorizer interface {
	AuthorizeGrantAdmin(ctx context.Context, actor identity.ActorRef, principal identity.PrincipalRef, action AdminAction) error
	AuthorizeDelegationAdmin(ctx context.Context, actor identity.ActorRef, ownerTeamID string, action AdminAction) error
}

// GrantRequest — запрос оценки. Не содержит владельца/окружения цели: они берутся из каталога.
type GrantRequest struct {
	Principal   identity.PrincipalRef
	OperationID string
	ActionID    string
	Target      TargetRef
}

// GrantDecision — результат оценки Kubron grants/delegation.
//
// Granted=true означает ТОЛЬКО: технические grants + обязательная Team delegation + границы тенантности
// разрешают кортеж. Это не итоговое разрешение на вызов Kubernetes, не bearer-токен и не должно
// передаваться/храниться как доказательство доступа: каждая защищённая операция обязана оценивать заново.
//
// Granted=true гарантированно только при err == nil; при любой ошибке Granted=false.
// Для ServicePrincipal положительное решение содержит ID и revision одной delegation,
// подтверждённой повторным чтением через DelegationReader.GetDelegation. Для TeamPrincipal
// delegation не требуется, поэтому оба поля остаются нулевыми. Revision фиксирует наблюдённую
// версию на момент Evaluate, но не резервирует delegation и не закрывает гонку до Plan Commit:
// commit обязан атомарно проверить эту версию через CAS/fencing.
type GrantDecision struct {
	Granted            bool
	ReasonCode         string
	GrantIDs           []string // отсортированы; GrantRevisions[i] — revision GrantIDs[i]
	GrantRevisions     []uint64
	DelegationID       string // для ServicePrincipal; при нескольких подходящих — наименьший ID
	DelegationRevision uint64 // revision именно DelegationID, подтверждённая повторным чтением
}

// Evaluator — вычисление Kubron grant eligibility.
type Evaluator interface {
	EvaluateKubronGrant(ctx context.Context, req GrantRequest) (GrantDecision, error)
}
