// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package identity

// Domain service: use cases над Backend с обязательным критичным аудитом (Contract Freeze §4, §7.2).
//
// Каждая защищённая мутация (create, suspend/resume, revoke, назначение/активация/отзыв binding):
//
//  1. проверяет запрос и полномочия человека-actor'а через внедрённый AdminAuthorizer — роль/владелец из UI
//     не принимаются: владелец выводится из PrincipalRef, а для существующих записей сверяется с хранимым;
//  2. строит неизменяемый audit.CriticalIntent (уникальный EventID на попытку, MutationID запроса,
//     канонический digest, ожидаемая revision, только непрозрачные ID — без секретов и display name);
//  3. durable AppendIntent; при ошибке Backend не вызывается;
//  4. выполняет ровно одну CAS-мутацию Backend (без повторов);
//  5. AppendOutcome (succeeded/failed/unknown). Если outcome не записан — audit.ErrOutcomeUnknown, успех не
//     сообщается, мутация не повторяется вслепую; запись остаётся непригодной.
//
// Запись пригодна (Resolve/ResolveBinding) только когда GetOutcome(orgID, LastAuditEventID) = succeeded.
//
// Идемпотентность: ID principal/binding детерминированно выводятся из владельца и MutationID запроса, поэтому
// повтор того же запроса попадает в ту же запись. Повтор уже применённой мутации не создаёт нового intent и
// не меняет revision; если её outcome потерян (ErrOutcomeUnknown), повтор доказательно (по LastMutationID и
// LastAuditEventID записи) завершает outcome — это и есть безопасная reconciliation без повторной мутации.
//
// Service не хранит состояния между вызовами и потокобезопасен, если безопасны его зависимости.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
)

// AdminAction — административное действие над identity (Contract Freeze §4).
type AdminAction string

const (
	AdminCreate           AdminAction = "create"
	AdminUpdate           AdminAction = "update"
	AdminSuspend          AdminAction = "suspend"
	AdminRevoke           AdminAction = "revoke"
	AdminAssignCredential AdminAction = "assign-credential"
)

// AdminAuthorizer проверяет, вправе ли actor выполнить действие над identity этого владельца. Реализуется
// интегратором поверх app-RBAC (Organization Admin — Service identity своей организации, Team Admin —
// Team identity своей команды). Возврат: nil — разрешено; ErrForbidden — запрещено; ErrUnavailable или
// любая другая ошибка — зависимость недоступна (fail closed). Права не наследуются от создателя identity.
type AdminAuthorizer interface {
	AuthorizeIdentityAdmin(ctx context.Context, actor ActorRef, action AdminAction, owner OwnerScope) error
}

// ClusterOwnerResolver возвращает владельца кластера из доверенного серверного каталога (не из запроса клиента).
// Владелец кластера — команда (OwnerTeam). Неизвестный кластер — ErrNotFound; недоступность — ErrUnavailable.
//
// Интерфейс добавлен Lane A: Contract Freeze §4 требует «trusted server-side cluster ownership resolution» для
// привязок, но не задаёт его сигнатуру. Адаптер к каталогу кластеров — зона integration owner.
type ClusterOwnerResolver interface {
	ResolveClusterOwner(ctx context.Context, clusterID string) (OwnerScope, error)
}

// Option настраивает Service.
type Option func(*Service)

// WithClock задаёт часы Service (по умолчанию time.Now).
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithEventIDs задаёт генератор уникальных EventID попыток (по умолчанию 128 бит crypto/rand).
func WithEventIDs(gen func() (string, error)) Option {
	return func(s *Service) {
		if gen != nil {
			s.newEventID = gen
		}
	}
}

// Service — use cases identity и binding.
type Service struct {
	backend    Backend
	rec        audit.CriticalRecorder
	authz      AdminAuthorizer
	clusters   ClusterOwnerResolver
	now        func() time.Time
	newEventID func() (string, error)
}

// NewService собирает Service. Любая nil-зависимость — ошибка (fail closed, никаких «разрешающих» заглушек).
func NewService(b Backend, rec audit.CriticalRecorder, authz AdminAuthorizer, clusters ClusterOwnerResolver, opts ...Option) (*Service, error) {
	switch {
	case b == nil:
		return nil, invalid("Backend")
	case rec == nil:
		return nil, invalid("CriticalRecorder")
	case authz == nil:
		return nil, invalid("AdminAuthorizer")
	case clusters == nil:
		return nil, invalid("ClusterOwnerResolver")
	}
	s := &Service{backend: b, rec: rec, authz: authz, clusters: clusters, now: time.Now, newEventID: randomEventID}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func randomEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "evt-" + hex.EncodeToString(b[:]), nil
}

// ---------- запросы ----------

// CreateRequest — создание технической identity. ID назначается сервером (выводится из MutationID).
type CreateRequest struct {
	Actor       ActorRef
	Kind        PrincipalKind // team или service
	OrgID       string
	TeamID      string // обязателен для team, пуст для service
	DisplayName string
	MutationID  string // ключ идемпотентности запроса, выбирается вызывающим (например UUID)
}

// TransitionRequest — смена состояния identity с CAS по ExpectedRevision (> 0).
type TransitionRequest struct {
	Actor            ActorRef
	Ref              PrincipalRef
	ExpectedRevision uint64
	MutationID       string
}

// AssignBindingRequest — назначение principal кластеру (создаёт pending-привязку).
type AssignBindingRequest struct {
	Actor      ActorRef
	Principal  PrincipalRef
	ClusterID  string
	MutationID string
}

// BindingTransitionRequest — активация/отзыв привязки (principal, cluster) с CAS.
type BindingTransitionRequest struct {
	Actor            ActorRef
	Principal        PrincipalRef
	ClusterID        string
	ExpectedRevision uint64
	MutationID       string
}

// ---------- use cases: identity ----------

// Create создаёт active Team/Service identity. Создание не выдаёт credentials, grants и Kubernetes-прав.
// Идемпотентно до следующей мутации записи: повтор с тем же MutationID и содержимым возвращает ту же identity,
// с другим содержимым — ErrConflict.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Identity, error) {
	name := strings.TrimSpace(req.DisplayName)
	if !validID(req.MutationID) {
		return Identity{}, invalid("MutationID")
	}
	if !validDisplayName(name) {
		return Identity{}, invalid("DisplayName")
	}
	ref := PrincipalRef{Kind: req.Kind, OrgID: req.OrgID, TeamID: req.TeamID}
	switch req.Kind {
	case PrincipalTeam:
		ref.ID = derivedID("tid-", string(req.Kind), req.OrgID, req.TeamID, req.MutationID)
	case PrincipalService:
		ref.ID = derivedID("sid-", string(req.Kind), req.OrgID, req.TeamID, req.MutationID)
	default:
		return Identity{}, invalid("Kind")
	}
	owner, err := ref.Owner() // валидирует ссылку и выводит владельца
	if err != nil {
		return Identity{}, err
	}
	if err := s.authorize(ctx, req.Actor, AdminCreate, owner); err != nil {
		return Identity{}, err
	}

	cur, err := s.backend.Get(ctx, ref)
	switch {
	case err == nil:
		if cur.Revision == 1 && cur.LastMutationID == req.MutationID && cur.DisplayName == name &&
			cur.CreatedBy == req.Actor && cur.Owner == owner {
			return s.replayed(ctx, cur, cur.Ref.OrgID, cur.LastAuditEventID, cur.Revision)
		}
		return Identity{}, fmt.Errorf("%w: %s", ErrConflict, "identity exists")
	case !errors.Is(err, ErrNotFound):
		return Identity{}, normalize(err)
	}

	var out Identity
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "identity.create", resourceType: "identity", resourceID: ref.ID,
		principal: ref, mutationID: req.MutationID, expectedRev: 0,
		digest: requestDigest("identity.create", string(ref.Kind), ref.ID, ref.OrgID, ref.TeamID, name,
			string(req.Actor.Kind), req.Actor.ID, req.MutationID),
	}, func(eventID string) (uint64, string, error) {
		got, err := s.backend.Create(ctx, Identity{
			Ref: ref, Owner: owner, DisplayName: name, State: IdentityActive,
			CreatedBy: req.Actor, CreatedAt: s.now(), LastMutationID: req.MutationID, LastAuditEventID: eventID,
		})
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// Suspend: active → suspended.
func (s *Service) Suspend(ctx context.Context, req TransitionRequest) (Identity, error) {
	return s.transition(ctx, req, IdentitySuspended, AdminSuspend, "identity.suspend")
}

// Resume: suspended → active. Revoked необратимо: повторное включение — только новая identity.
func (s *Service) Resume(ctx context.Context, req TransitionRequest) (Identity, error) {
	return s.transition(ctx, req, IdentityActive, AdminUpdate, "identity.resume")
}

// Revoke: active|suspended → revoked (терминально).
func (s *Service) Revoke(ctx context.Context, req TransitionRequest) (Identity, error) {
	return s.transition(ctx, req, IdentityRevoked, AdminRevoke, "identity.revoke")
}

func (s *Service) transition(ctx context.Context, req TransitionRequest, next IdentityState,
	action AdminAction, auditAction string) (Identity, error) {
	if !validID(req.MutationID) {
		return Identity{}, invalid("MutationID")
	}
	if req.ExpectedRevision == 0 {
		return Identity{}, invalid("ExpectedRevision")
	}
	owner, err := req.Ref.Owner()
	if err != nil {
		return Identity{}, err
	}
	if err := s.authorize(ctx, req.Actor, action, owner); err != nil {
		return Identity{}, err
	}
	cur, err := s.backend.Get(ctx, req.Ref)
	if err != nil {
		return Identity{}, normalize(err)
	}
	if cur.Owner != owner {
		// Хранимый владелец обязан совпадать с выведенным из ссылки; расхождение — нарушенный инвариант.
		return Identity{}, fmt.Errorf("%w: %s", ErrUnavailable, "stored owner mismatch")
	}
	if cur.LastMutationID == req.MutationID {
		if cur.State == next && cur.Revision == req.ExpectedRevision+1 {
			return s.replayed(ctx, cur, cur.Ref.OrgID, cur.LastAuditEventID, cur.Revision)
		}
		return Identity{}, fmt.Errorf("%w: %s", ErrConflict, "mutation id reused")
	}

	var out Identity
	err = s.protect(ctx, protected{
		actor: req.Actor, action: auditAction, resourceType: "identity", resourceID: req.Ref.ID,
		principal: req.Ref, mutationID: req.MutationID, expectedRev: req.ExpectedRevision,
		digest: requestDigest(auditAction, string(req.Ref.Kind), req.Ref.ID, req.Ref.OrgID, req.Ref.TeamID,
			string(next), fmt.Sprint(req.ExpectedRevision), string(req.Actor.Kind), req.Actor.ID, req.MutationID),
	}, func(eventID string) (uint64, string, error) {
		got, err := s.backend.SetState(ctx, req.Ref, req.ExpectedRevision, next, req.MutationID, eventID)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// UpdateDisplayName changes only the display name. Ownership and all identity/lifecycle metadata remain immutable.
func (s *Service) UpdateDisplayName(ctx context.Context, actor ActorRef, ref PrincipalRef,
	expectedRevision uint64, displayName, mutationID string) (Identity, error) {
	name := strings.TrimSpace(displayName)
	if !validID(mutationID) {
		return Identity{}, invalid("MutationID")
	}
	if expectedRevision == 0 {
		return Identity{}, invalid("ExpectedRevision")
	}
	if !validDisplayName(name) {
		return Identity{}, invalid("DisplayName")
	}
	owner, err := ref.Owner()
	if err != nil {
		return Identity{}, err
	}
	if err := s.authorize(ctx, actor, AdminUpdate, owner); err != nil {
		return Identity{}, err
	}
	cur, err := s.backend.Get(ctx, ref)
	if err != nil {
		return Identity{}, normalize(err)
	}
	if cur.Owner != owner {
		return Identity{}, fmt.Errorf("%w: %s", ErrUnavailable, "stored owner mismatch")
	}
	if cur.LastMutationID == mutationID {
		if cur.DisplayName == name && cur.Revision == expectedRevision+1 {
			return s.replayed(ctx, cur, cur.Ref.OrgID, cur.LastAuditEventID, cur.Revision)
		}
		return Identity{}, fmt.Errorf("%w: %s", ErrConflict, "mutation id reused")
	}

	var out Identity
	err = s.protect(ctx, protected{
		actor: actor, action: "identity.display_name.update", resourceType: "identity",
		resourceID: ref.ID, principal: ref, mutationID: mutationID, expectedRev: expectedRevision,
		digest: requestDigest("identity.display_name.update", string(ref.Kind), ref.ID,
			ref.OrgID, ref.TeamID, name, fmt.Sprint(expectedRevision),
			string(actor.Kind), actor.ID, mutationID),
	}, func(eventID string) (uint64, string, error) {
		got, err := s.backend.UpdateDisplayName(ctx, ref, expectedRevision, name, mutationID, eventID)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// ---------- use cases: binding ----------

// AssignBinding назначает principal кластеру: создаёт pending-привязку, которая резервирует кортеж, но не
// может использоваться. Владелец кластера берётся из доверенного каталога: Team principal привязывается
// только к кластеру своей команды; Service principal — к кластеру своей организации, и сама привязка не
// даёт межкомандного доступа. Привязка не создаёт grant и не содержит секретов.
func (s *Service) AssignBinding(ctx context.Context, req AssignBindingRequest) (CredentialBinding, error) {
	if !validID(req.MutationID) {
		return CredentialBinding{}, invalid("MutationID")
	}
	if !validID(req.ClusterID) {
		return CredentialBinding{}, invalid("ClusterID")
	}
	owner, err := req.Principal.Owner()
	if err != nil {
		return CredentialBinding{}, err
	}
	if err := s.authorize(ctx, req.Actor, AdminAssignCredential, owner); err != nil {
		return CredentialBinding{}, err
	}
	if err := s.checkClusterOwnership(ctx, req.Principal, req.ClusterID); err != nil {
		return CredentialBinding{}, err
	}
	p := req.Principal
	bindingID := derivedID("bnd-", string(p.Kind), p.ID, p.OrgID, p.TeamID, req.ClusterID, req.MutationID)

	cur, err := s.backend.GetBinding(ctx, p, req.ClusterID)
	switch {
	case err == nil:
		if cur.ID == bindingID && cur.Revision == 1 && cur.LastMutationID == req.MutationID && cur.CreatedBy == req.Actor {
			return s.replayedBinding(ctx, cur)
		}
		// Иная живая привязка: решит Backend (ErrConflict) и это попадёт в аудит как failed.
	case errors.Is(err, ErrRevoked), errors.Is(err, ErrNotFound):
		// живой привязки нет — продолжаем
	default:
		return CredentialBinding{}, normalize(err) // в т.ч. неоднозначность: отказ
	}

	var out CredentialBinding
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "binding.create", resourceType: "binding", resourceID: bindingID,
		principal: p, clusterID: req.ClusterID, mutationID: req.MutationID, expectedRev: 0,
		digest: requestDigest("binding.create", string(p.Kind), p.ID, p.OrgID, p.TeamID, req.ClusterID, bindingID,
			string(req.Actor.Kind), req.Actor.ID, req.MutationID),
	}, func(eventID string) (uint64, string, error) {
		got, err := s.backend.PutBinding(ctx, CredentialBinding{
			ID: bindingID, Principal: p, ClusterID: req.ClusterID, State: BindingPending,
			CreatedBy: req.Actor, CreatedAt: s.now(),
		}, 0, req.MutationID, eventID)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return CredentialBinding{}, err
	}
	return out, nil
}

// ActivateBinding: pending → active. Вызывать только после того, как credential долговечно сохранён
// (ответственность credential-слоя). Привязка пригодна не сразу, а после подтверждения её аудита.
func (s *Service) ActivateBinding(ctx context.Context, req BindingTransitionRequest) (CredentialBinding, error) {
	return s.transitionBinding(ctx, req, BindingActive, "binding.activate")
}

// RevokeBinding: pending|active → revoked (терминально). Допустим и для приостановленной/отозванной identity.
func (s *Service) RevokeBinding(ctx context.Context, req BindingTransitionRequest) (CredentialBinding, error) {
	return s.transitionBinding(ctx, req, BindingRevoked, "binding.revoke")
}

func (s *Service) transitionBinding(ctx context.Context, req BindingTransitionRequest, next BindingState,
	auditAction string) (CredentialBinding, error) {
	if !validID(req.MutationID) {
		return CredentialBinding{}, invalid("MutationID")
	}
	if !validID(req.ClusterID) {
		return CredentialBinding{}, invalid("ClusterID")
	}
	if req.ExpectedRevision == 0 {
		return CredentialBinding{}, invalid("ExpectedRevision")
	}
	owner, err := req.Principal.Owner()
	if err != nil {
		return CredentialBinding{}, err
	}
	if err := s.authorize(ctx, req.Actor, AdminAssignCredential, owner); err != nil {
		return CredentialBinding{}, err
	}
	// При ErrRevoked Backend отдаёт самую позднюю отозванную привязку: нужна для идемпотентного повтора
	// отзыва; иная мутация над ней закончится ErrRevoked на стороне Backend.
	cur, err := s.backend.GetBinding(ctx, req.Principal, req.ClusterID)
	if err != nil && !errors.Is(err, ErrRevoked) {
		return CredentialBinding{}, normalize(err)
	}
	if cur.LastMutationID == req.MutationID {
		if cur.State == next && cur.Revision == req.ExpectedRevision+1 {
			return s.replayedBinding(ctx, cur)
		}
		return CredentialBinding{}, fmt.Errorf("%w: %s", ErrConflict, "mutation id reused")
	}

	p := req.Principal
	var out CredentialBinding
	err = s.protect(ctx, protected{
		actor: req.Actor, action: auditAction, resourceType: "binding", resourceID: cur.ID,
		principal: p, clusterID: req.ClusterID, mutationID: req.MutationID, expectedRev: req.ExpectedRevision,
		digest: requestDigest(auditAction, string(p.Kind), p.ID, p.OrgID, p.TeamID, req.ClusterID, cur.ID,
			string(next), fmt.Sprint(req.ExpectedRevision), string(req.Actor.Kind), req.Actor.ID, req.MutationID),
	}, func(eventID string) (uint64, string, error) {
		var got CredentialBinding
		var err error
		if next == BindingActive {
			got, err = s.backend.ActivateBinding(ctx, cur.ID, req.ExpectedRevision, req.MutationID, eventID)
		} else {
			got, err = s.backend.RevokeBinding(ctx, cur.ID, req.ExpectedRevision, req.MutationID, eventID)
		}
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return CredentialBinding{}, err
	}
	return out, nil
}

// checkClusterOwnership сверяет принадлежность кластера по доверенному каталогу.
func (s *Service) checkClusterOwnership(ctx context.Context, p PrincipalRef, clusterID string) error {
	owner, err := s.clusters.ResolveClusterOwner(ctx, clusterID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if owner.Validate() != nil || owner.Kind != OwnerTeam {
		return fmt.Errorf("%w: %s", ErrUnavailable, "cluster owner is not resolvable")
	}
	if owner.OrgID != p.OrgID {
		return ErrNotFound // чужая организация неотличима от отсутствующего кластера
	}
	if p.Kind == PrincipalTeam && owner.TeamID != p.TeamID {
		return invalid("binding cluster owner")
	}
	return nil
}

// ---------- чтение ----------

// Get возвращает identity в любом состоянии (включая revoked) как метаданные. Это чтение для доверенного
// серверного кода: проверка прав на просмотр (app-RBAC) выполняется транспортным слоем до вызова, в замороженном
// контракте отдельного AdminAction для чтения нет. Для решения «можно ли использовать» служит Resolve.
func (s *Service) Get(ctx context.Context, ref PrincipalRef) (Identity, error) {
	got, err := s.backend.Get(ctx, ref)
	if err != nil {
		return Identity{}, normalize(err)
	}
	return got, nil
}

// List возвращает identity ровно этого владельца (см. Get о проверке прав на чтение).
func (s *Service) List(ctx context.Context, owner OwnerScope) ([]Identity, error) {
	got, err := s.backend.List(ctx, owner)
	if err != nil {
		return nil, normalize(err)
	}
	return got, nil
}

// ListBindings возвращает все привязки principal (см. Get о проверке прав на чтение).
func (s *Service) ListBindings(ctx context.Context, principal PrincipalRef) ([]CredentialBinding, error) {
	got, err := s.backend.ListBindings(ctx, principal)
	if err != nil {
		return nil, normalize(err)
	}
	return got, nil
}

// Resolve возвращает identity только если она ПРИГОДНА: active и аудит её последней мутации подтверждён как
// succeeded. Отозванная — ErrRevoked; приостановленная, с отсутствующим/неуспешным/неизвестным аудитом —
// ErrForbidden; недоступность хранилища или аудита — ErrUnavailable. Единственный путь, которым потребители
// (credential, authorization) вправе принимать identity как действующую.
func (s *Service) Resolve(ctx context.Context, ref PrincipalRef) (Identity, error) {
	cur, err := s.backend.Get(ctx, ref)
	if err != nil {
		return Identity{}, normalize(err)
	}
	switch cur.State {
	case IdentityActive:
	case IdentityRevoked:
		return Identity{}, fmt.Errorf("%w: %s", ErrRevoked, "identity")
	default:
		return Identity{}, deny("identity is not active")
	}
	if err := s.outcomeSucceeded(ctx, cur.Ref.OrgID, cur.LastAuditEventID); err != nil {
		return Identity{}, err
	}
	return cur, nil
}

// ResolveBinding возвращает привязку только если пригодны и principal (Resolve), и сама привязка: состояние
// active и подтверждённый аудит. Pending — ErrForbidden, revoked — ErrRevoked, неоднозначность — отказ.
func (s *Service) ResolveBinding(ctx context.Context, principal PrincipalRef, clusterID string) (CredentialBinding, error) {
	if _, err := s.Resolve(ctx, principal); err != nil {
		return CredentialBinding{}, err
	}
	cur, err := s.backend.GetBinding(ctx, principal, clusterID)
	if err != nil {
		return CredentialBinding{}, normalize(err)
	}
	if cur.State != BindingActive {
		return CredentialBinding{}, deny("binding is not active")
	}
	if err := s.outcomeSucceeded(ctx, principal.OrgID, cur.LastAuditEventID); err != nil {
		return CredentialBinding{}, err
	}
	return cur, nil
}

// ---------- протокол критичной мутации ----------

type protected struct {
	actor        ActorRef
	action       string // значение audit.CriticalIntent.Action
	resourceType string
	resourceID   string
	principal    PrincipalRef
	clusterID    string
	mutationID   string
	digest       string
	expectedRev  uint64
}

// protect исполняет шаги 2–5 протокола §7.2. apply вызывается не более одного раза и только после durable
// AppendIntent; возвращает revision результата и LastAuditEventID полученной записи.
func (s *Service) protect(ctx context.Context, p protected, apply func(eventID string) (uint64, string, error)) error {
	eventID, err := s.newEventID()
	if err != nil || !validID(eventID) {
		return fmt.Errorf("%w: %s", ErrUnavailable, "event id generation")
	}
	intent := audit.CriticalIntent{
		EventID: eventID, MutationID: p.mutationID, RequestDigest: p.digest,
		ActorKind: string(p.actor.Kind), ActorID: p.actor.ID,
		PrincipalKind: string(p.principal.Kind), PrincipalID: p.principal.ID,
		OrgID: p.principal.OrgID, TeamID: p.principal.TeamID, ClusterID: p.clusterID,
		Action: p.action, ResourceType: p.resourceType, ResourceID: p.resourceID,
		ExpectedRevision: p.expectedRev, OccurredAt: s.now(),
	}
	if err := s.rec.AppendIntent(ctx, intent); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err) // Backend не вызывался
	}

	// Исход фиксируется даже если вызывающий отменил ctx уже после мутации.
	octx := context.WithoutCancel(ctx)
	rev, lastEvent, mutErr := apply(eventID)
	if mutErr != nil {
		mutErr = normalize(mutErr)
		if aerr := s.rec.AppendOutcome(octx, failureOutcome(eventID, mutErr, s.now())); aerr != nil {
			return errors.Join(fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr), mutErr)
		}
		return mutErr
	}
	if lastEvent != eventID {
		// Идентичный запрос успел примениться параллельно и Backend вернул его запись (идемпотентный повтор):
		// эта попытка состояние не меняла.
		out := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalFailed, ResourceRevision: rev,
			ReasonCode: "replayed", CompletedAt: s.now()}
		if aerr := s.rec.AppendOutcome(octx, out); aerr != nil {
			return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr)
		}
		return s.confirm(octx, p.principal.OrgID, lastEvent, rev)
	}
	out := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalSucceeded, ResourceRevision: rev, CompletedAt: s.now()}
	if aerr := s.rec.AppendOutcome(octx, out); aerr != nil {
		return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr)
	}
	return nil
}

// failureOutcome: определённые отказы Backend (состояние точно не изменилось) — failed; неясный исход
// (недоступность хранилища) — unknown.
func failureOutcome(eventID string, err error, now time.Time) audit.CriticalOutcome {
	o := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalFailed, CompletedAt: now}
	switch {
	case errors.Is(err, ErrConflict):
		o.ReasonCode = "conflict"
	case errors.Is(err, ErrInvalid):
		o.ReasonCode = "invalid"
	case errors.Is(err, ErrNotFound):
		o.ReasonCode = "not_found"
	case errors.Is(err, ErrRevoked):
		o.ReasonCode = "revoked"
	case errors.Is(err, ErrForbidden):
		o.ReasonCode = "forbidden"
	default:
		o.Result = audit.CriticalUnknown
		o.ReasonCode = "backend_unavailable"
	}
	return o
}

// confirm доказательно завершает аудит уже применённой мутации: запись несёт LastMutationID запроса и
// LastAuditEventID этой попытки, значит мутация выполнена. Нет outcome — записывается succeeded; есть
// succeeded — ничего; иной outcome противоречит состоянию записи → ErrOutcomeUnknown (нужна ручная сверка).
func (s *Service) confirm(ctx context.Context, orgID, eventID string, revision uint64) error {
	ctx = context.WithoutCancel(ctx)
	got, err := s.rec.GetOutcome(ctx, orgID, eventID)
	switch {
	case err == nil:
		if got.Result == audit.CriticalSucceeded {
			return nil
		}
		return fmt.Errorf("%w: %s", audit.ErrOutcomeUnknown, "recorded outcome contradicts resource state")
	case errors.Is(err, audit.ErrCriticalNotFound):
		out := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalSucceeded, ResourceRevision: revision, CompletedAt: s.now()}
		if aerr := s.rec.AppendOutcome(ctx, out); aerr != nil {
			return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr)
		}
		return nil
	default:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
}

func (s *Service) replayed(ctx context.Context, cur Identity, orgID, eventID string, rev uint64) (Identity, error) {
	if err := s.confirm(ctx, orgID, eventID, rev); err != nil {
		return Identity{}, err
	}
	return cur, nil
}

func (s *Service) replayedBinding(ctx context.Context, cur CredentialBinding) (CredentialBinding, error) {
	if err := s.confirm(ctx, cur.Principal.OrgID, cur.LastAuditEventID, cur.Revision); err != nil {
		return CredentialBinding{}, err
	}
	return cur, nil
}

// outcomeSucceeded — «пригодность» записи: подтверждённый succeeded outcome её последнего критичного события.
// Отсутствующий, failed или unknown исход — отказ; недоступность аудита — ErrUnavailable (fail closed).
func (s *Service) outcomeSucceeded(ctx context.Context, orgID, eventID string) error {
	got, err := s.rec.GetOutcome(ctx, orgID, eventID)
	switch {
	case err == nil:
		if got.Result == audit.CriticalSucceeded {
			return nil
		}
		return deny("critical audit outcome is not succeeded")
	case errors.Is(err, audit.ErrCriticalNotFound):
		return deny("critical audit outcome is not confirmed")
	default:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
}

// ---------- вспомогательное ----------

// authorize проверяет actor и административные полномочия. Административные мутации выполняет только
// аутентифицированный человек (ActorUser): system/scheduler и identity-principal'ы не администрируют identity.
func (s *Service) authorize(ctx context.Context, actor ActorRef, action AdminAction, owner OwnerScope) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if actor.Kind != ActorUser {
		return deny("administrative actions require a user actor")
	}
	err := s.authz.AuthorizeIdentityAdmin(ctx, actor, action, owner)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrForbidden):
		return deny("administrative action is not permitted")
	default:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
}

func deny(reason string) error { return fmt.Errorf("%w: %s", ErrForbidden, reason) }

// normalize приводит ошибку зависимости к sentinel'ам пакета: известные проходят как есть, прочие означают
// недоступность (fail closed).
func normalize(err error) error {
	for _, target := range []error{ErrNotFound, ErrConflict, ErrInvalid, ErrForbidden, ErrRevoked, ErrUnavailable} {
		if errors.Is(err, target) {
			return err
		}
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// derivedID детерминированно выводит непрозрачный ID из частей (с длинами — без неоднозначной склейки).
func derivedID(prefix string, parts ...string) string {
	sum := sha256.New()
	var n [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		sum.Write(n[:])
		sum.Write([]byte(p))
	}
	return prefix + hex.EncodeToString(sum.Sum(nil)[:16])
}

// requestDigest — канонический digest запроса для CriticalIntent.RequestDigest: sha256 над частями с длинами.
func requestDigest(parts ...string) string {
	sum := sha256.New()
	var n [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		sum.Write(n[:])
		sum.Write([]byte(p))
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}
