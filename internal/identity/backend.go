// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import "context"

// Backend — узкий контракт хранилища identity и binding (Contract Freeze §4). Реализации: File (этот пакет,
// Desktop, один процесс) и PostgreSQL-адаптер интегратора; обе обязаны проходить identitytest.Run.
//
// Backend проверяет структурные инварианты, неизменяемость владельца, revision/CAS и переходы состояний.
// Он НЕ проверяет полномочия actor'а и НЕ пишет критичный аудит — это делает Service. Backend не хранит
// секреты и не содержит операций «мягкого» перезаписывания: last-write-wins недопустим.
//
// Общие правила:
//   - Ссылка PrincipalRef сопоставляется целиком (Kind, ID, OrgID, TeamID). Чужая организация/команда или
//     другой вид с тем же ID отвечает ErrNotFound — неотличимо от отсутствующей записи.
//   - UserPrincipal не хранится: любой метод с Kind=user отвечает ErrInvalid.
//   - Уникальность principal — по (Kind, ID); ID не переиспользуется никогда (hard delete нет).
//   - expectedRevision == 0 не является wildcard: для обновлений это ErrInvalid; для PutBinding нуль
//     означает «записи не должно быть» (создание).
//   - Идемпотентность: повтор мутации с тем же mutationID, уже применённой последней к записи (и совпадающей
//     по содержимому и expectedRevision+1 == Revision), возвращает текущую запись без изменений. Тот же
//     mutationID с другим содержимым — ErrConflict. Помнится только последняя мутация записи.
//   - Каждая успешная мутация увеличивает Revision ровно на 1 и сохраняет LastMutationID/LastAuditEventID.
//     Время (CreatedAt/UpdatedAt) для обновлений назначает Backend (File — свои часы, PostgreSQL — время БД).
//   - Любая ошибка хранилища, после которой исход записи нельзя доказать, — ErrUnavailable (fail closed).
//     Отменённый ctx до начала записи — тоже ErrUnavailable, состояние не меняется.
//
// Результат метода при ошибке — нулевое значение, кроме GetBinding с ErrRevoked (см. там).
type Backend interface {
	// Create создаёт новую active identity. in.Revision должен быть 0, in.State — active; Backend назначает
	// Revision=1 и UpdatedAt=CreatedAt. Принимает только Team/Service. Повтор Create существующей записи —
	// ErrConflict, кроме идемпотентного повтора (тот же LastMutationID, Revision==1, то же Ref/Owner/
	// DisplayName/CreatedBy).
	Create(ctx context.Context, in Identity) (Identity, error)

	// Get возвращает identity в любом состоянии (в т.ч. revoked: состояние — данные, а не ошибка).
	Get(ctx context.Context, ref PrincipalRef) (Identity, error)

	// List возвращает identity ровно этого владельца (Organization — только Service-identity организации,
	// без Team-identity её команд), упорядоченные по (Kind, ID). Владелец не наследуется вниз.
	List(ctx context.Context, owner OwnerScope) ([]Identity, error)

	// SetState выполняет один CAS-переход состояния. Порядок проверок: идемпотентный повтор → revision
	// (ErrConflict) → допустимость перехода (ErrRevoked из revoked, ErrInvalid иначе). Владелец не меняется.
	SetState(ctx context.Context, ref PrincipalRef, expectedRevision uint64,
		next IdentityState, mutationID, auditEventID string) (Identity, error)

	// UpdateDisplayName изменяет только человекочитаемое имя. Использует CAS и mutation/audit IDs.
	UpdateDisplayName(ctx context.Context, ref PrincipalRef, expectedRevision uint64,
		displayName, mutationID, auditEventID string) (Identity, error)

	// PutBinding создаёт pending-привязку (in.State=pending, in.Revision=0, expectedRevision=0). Принципал
	// должен существовать и быть active (revoked → ErrRevoked, suspended → ErrInvalid). Не более одной
	// pending/active привязки на (principal, cluster): иначе ErrConflict. ID привязки не переиспользуется
	// (в т.ч. другим principal): ErrConflict. После revoke новая привязка с новым ID допустима.
	// in.LastMutationID/LastAuditEventID пусты либо равны параметрам; источник истины — параметры.
	PutBinding(ctx context.Context, in CredentialBinding,
		expectedRevision uint64, mutationID, auditEventID string) (CredentialBinding, error)

	// GetBinding возвращает единственную pending/active привязку кортежа. Если живой нет, но были
	// отозванные — самую позднюю из них вместе с ErrRevoked (для provenance и идемпотентного повтора;
	// вызывающий обязан считать её непригодной). Нет ни одной — ErrNotFound. Больше одной живой
	// (нарушенный инвариант) — ErrConflict: неоднозначность = отказ.
	GetBinding(ctx context.Context, principal PrincipalRef, clusterID string) (CredentialBinding, error)

	// ListBindings возвращает все привязки principal (любых состояний), упорядоченные по (ClusterID,
	// CreatedAt, ID). Привязки других principal не попадают в выдачу.
	ListBindings(ctx context.Context, principal PrincipalRef) ([]CredentialBinding, error)

	// ActivateBinding: pending → active (CAS). Принципал должен быть active. Вызывать можно только после
	// того, как credential долговечно сохранён (ответственность вызывающего, Lane B).
	ActivateBinding(ctx context.Context, id string, expectedRevision uint64,
		mutationID, auditEventID string) (CredentialBinding, error)

	// RevokeBinding: pending|active → revoked (CAS), терминально. Допустимо при любом состоянии principal,
	// чтобы администратор мог снять привязку отозванной/приостановленной identity.
	RevokeBinding(ctx context.Context, id string, expectedRevision uint64,
		mutationID, auditEventID string) (CredentialBinding, error)
}
