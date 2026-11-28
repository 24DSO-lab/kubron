// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

// Use cases мутаций grants и Cross-Team Delegation с обязательным критичным аудитом (Contract Freeze §6.3,
// §7.2). Каждая защищённая мутация:
//
//  1. проверяет запрос и полномочия человека-actor'а через внедрённый AdminAuthorizer (роль из UI не
//     принимается; управление delegation идёт ТОЛЬКО через AuthorizeDelegationAdmin для Team-владельца —
//     Organization Admin за Team её не выдаёт, и Service никогда не подменяет одну проверку другой);
//  2. проверяет принадлежность целей по доверенному каталогу (OwnershipResolver) и состояние principal'а;
//  3. строит неизменяемый audit.CriticalIntent (уникальный EventID попытки, MutationID, канонический
//     digest, ожидаемая revision, только непрозрачные ID);
//  4. durable AppendIntent; при ошибке Backend не вызывается;
//  5. выполняет ровно одну CAS-мутацию Backend (без повторов);
//  6. AppendOutcome (succeeded/failed/unknown). Если outcome не записан — audit.ErrOutcomeUnknown, успех не
//     сообщается, мутация не повторяется вслепую, запись остаётся непригодной для Evaluator'а.
//
// Запись пригодна для оценки только когда GetOutcome(orgID, LastAuditEventID) = succeeded (проверяет
// Evaluator). Идемпотентность: ID grant/delegation выводятся детерминированно из контекста и MutationID,
// поэтому повтор того же запроса попадает в ту же запись; повтор уже применённой мутации не создаёт нового
// intent и не меняет revision. Если её outcome потерян, повтор доказательно (по LastMutationID и
// LastAuditEventID записи) завершает outcome — безопасная reconciliation без повторной мутации.
// Pending-intent reconciliation по ListPending — отдельный workflow интегратора (Amendment 01 §3), здесь нет
// ни демона, ни автоматических повторов.
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
	"strconv"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/identity"
)

// Option настраивает Service.
type Option func(*Service)

// WithClock задаёт часы Service (по умолчанию time.Now).
func WithClock(c Clock) Option {
	return func(s *Service) {
		if c != nil {
			s.clock = c
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

// Service — use cases grants и delegation.
type Service struct {
	grants     GrantBackend
	delegs     DelegationBackend
	rec        audit.CriticalRecorder
	authz      AdminAuthorizer
	owners     OwnershipResolver
	principals IdentityStateReader
	clock      Clock
	newEventID func() (string, error)
}

// NewService собирает Service. Любая nil-зависимость — ошибка (fail closed).
func NewService(grants GrantBackend, delegs DelegationBackend, rec audit.CriticalRecorder, authz AdminAuthorizer,
	owners OwnershipResolver, principals IdentityStateReader, opts ...Option) (*Service, error) {
	switch {
	case grants == nil:
		return nil, invalid("GrantBackend")
	case delegs == nil:
		return nil, invalid("DelegationBackend")
	case rec == nil:
		return nil, invalid("CriticalRecorder")
	case authz == nil:
		return nil, invalid("AdminAuthorizer")
	case owners == nil:
		return nil, invalid("OwnershipResolver")
	case principals == nil:
		return nil, invalid("IdentityStateReader")
	}
	s := &Service{grants: grants, delegs: delegs, rec: rec, authz: authz, owners: owners, principals: principals,
		clock: ClockFunc(time.Now), newEventID: randomEventID}
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

// CreateGrantRequest — выдача grant. ID назначается сервером (выводится из MutationID).
type CreateGrantRequest struct {
	Actor       identity.ActorRef
	Principal   identity.PrincipalRef
	Actions     []OperationAction
	Targets     TargetScope
	Constraints ConstraintSet
	ExpiresAt   time.Time
	MutationID  string // ключ идемпотентности запроса, выбирается вызывающим (например UUID)
}

// UpdateGrantRequest — замена изменяемых полей active grant с CAS по ExpectedRevision (> 0). Principal
// берётся из хранимой записи, а не из запроса.
type UpdateGrantRequest struct {
	Actor            identity.ActorRef
	ID               string
	ExpectedRevision uint64
	Actions          []OperationAction
	Targets          TargetScope
	Constraints      ConstraintSet
	ExpiresAt        time.Time
	MutationID       string
}

// RevokeGrantRequest — отзыв grant (терминально).
type RevokeGrantRequest struct {
	Actor            identity.ActorRef
	ID               string
	ExpectedRevision uint64
	MutationID       string
}

// CreateDelegationRequest — выдача delegation Team-владельцем конкретному ServicePrincipal той же Organization.
type CreateDelegationRequest struct {
	Actor            identity.ActorRef
	OrgID            string
	OwnerTeamID      string
	ServicePrincipal identity.PrincipalRef
	Actions          []OperationAction
	Targets          TargetScope
	Constraints      ConstraintSet
	ExpiresAt        time.Time
	MutationID       string
}

// UpdateDelegationRequest — замена изменяемых полей delegation (состояние сохраняется).
type UpdateDelegationRequest struct {
	Actor            identity.ActorRef
	ID               string
	ExpectedRevision uint64
	Actions          []OperationAction
	Targets          TargetScope
	Constraints      ConstraintSet
	ExpiresAt        time.Time
	MutationID       string
}

// DelegationTransitionRequest — приостановка, возобновление либо отзыв delegation.
type DelegationTransitionRequest struct {
	Actor            identity.ActorRef
	ID               string
	ExpectedRevision uint64
	MutationID       string
}

const pendingEventID = "evt-pending" // подставляется только для проверки формы до выдачи реального EventID

// ---------- use cases: grants ----------

// CreateGrant выдаёт grant техническому principal'у. Выдача не создаёт credentials и не даёт прав Kubernetes.
func (s *Service) CreateGrant(ctx context.Context, req CreateGrantRequest) (Grant, error) {
	if !validID(req.MutationID) {
		return Grant{}, invalid("MutationID")
	}
	p := req.Principal
	if err := p.Validate(); err != nil || !p.IsTechnical() {
		return Grant{}, invalid("Principal")
	}
	g := Grant{
		ID: derivedID("grt-", string(p.Kind), p.ID, p.OrgID, p.TeamID, req.MutationID), Principal: p,
		Actions: req.Actions, Targets: req.Targets, Constraints: req.Constraints, State: GrantActive,
		ExpiresAt: req.ExpiresAt, CreatedBy: req.Actor, LastMutationID: req.MutationID, LastAuditEventID: pendingEventID,
	}.Canonical()
	if err := g.validate(false); err != nil {
		return Grant{}, err
	}
	if err := s.authorizeGrant(ctx, req.Actor, p, AdminGrantCreate); err != nil {
		return Grant{}, err
	}

	cur, err := s.grants.GetGrant(ctx, g.ID)
	switch {
	case err == nil:
		if cur.Revision == 1 && cur.LastMutationID == req.MutationID && cur.CreatedBy == req.Actor && sameGrantContent(cur, g) {
			return s.replayed(ctx, p.OrgID, cur.LastAuditEventID, cur.Revision, cur)
		}
		return Grant{}, conflict("grant exists")
	case !errors.Is(err, ErrNotFound):
		return Grant{}, normalize(err)
	}

	if err := s.principalUsable(ctx, p); err != nil {
		return Grant{}, err
	}
	if err := s.checkFuture(g.ExpiresAt); err != nil {
		return Grant{}, err
	}
	if err := s.checkGrantScope(ctx, p, g.Targets); err != nil {
		return Grant{}, err
	}

	var out Grant
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "grant.create", resourceType: "grant", resourceID: g.ID,
		principal: p, orgID: p.OrgID, teamID: p.TeamID, mutationID: req.MutationID, expectedRev: 0,
		digest: requestDigest(append([]string{"grant.create", g.ID, string(p.Kind), p.ID, p.OrgID, p.TeamID,
			string(req.Actor.Kind), req.Actor.ID, req.MutationID}, contentParts(g.Actions, g.Targets, g.Constraints, g.ExpiresAt)...)...),
	}, func(eventID string) (uint64, string, error) {
		in := g
		in.LastAuditEventID = eventID
		got, err := s.grants.CreateGrant(ctx, in)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

// UpdateGrant заменяет изменяемые поля active grant.
func (s *Service) UpdateGrant(ctx context.Context, req UpdateGrantRequest) (Grant, error) {
	if !validID(req.MutationID) {
		return Grant{}, invalid("MutationID")
	}
	if !validID(req.ID) {
		return Grant{}, invalid("ID")
	}
	if req.ExpectedRevision == 0 {
		return Grant{}, invalid("ExpectedRevision")
	}
	cur, err := s.grants.GetGrant(ctx, req.ID)
	if err != nil {
		return Grant{}, normalize(err)
	}
	if err := s.authorizeGrant(ctx, req.Actor, cur.Principal, AdminGrantUpdate); err != nil {
		return Grant{}, err
	}
	next := cur
	next.Actions, next.Targets, next.Constraints, next.ExpiresAt = req.Actions, req.Targets, req.Constraints, req.ExpiresAt
	next.State, next.Revision = GrantActive, 0
	next.LastMutationID, next.LastAuditEventID = req.MutationID, pendingEventID
	next = next.Canonical()
	if err := next.validate(false); err != nil {
		return Grant{}, err
	}

	if cur.LastMutationID == req.MutationID {
		if cur.State == GrantActive && cur.Revision == req.ExpectedRevision+1 && sameGrantContent(cur, next) {
			return s.replayed(ctx, cur.Principal.OrgID, cur.LastAuditEventID, cur.Revision, cur)
		}
		return Grant{}, conflict("mutation id reused")
	}
	if cur.State == GrantRevoked {
		return Grant{}, fmt.Errorf("%w: %s", ErrRevoked, "grant")
	}
	if err := s.checkFuture(next.ExpiresAt); err != nil {
		return Grant{}, err
	}
	if err := s.checkGrantScope(ctx, cur.Principal, next.Targets); err != nil {
		return Grant{}, err
	}

	p := cur.Principal
	var out Grant
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "grant.update", resourceType: "grant", resourceID: cur.ID,
		principal: p, orgID: p.OrgID, teamID: p.TeamID, mutationID: req.MutationID, expectedRev: req.ExpectedRevision,
		digest: requestDigest(append([]string{"grant.update", cur.ID, fmt.Sprint(req.ExpectedRevision),
			string(req.Actor.Kind), req.Actor.ID, req.MutationID}, contentParts(next.Actions, next.Targets, next.Constraints, next.ExpiresAt)...)...),
	}, func(eventID string) (uint64, string, error) {
		in := next
		in.LastAuditEventID = eventID
		got, err := s.grants.UpdateGrant(ctx, in, req.ExpectedRevision)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

// RevokeGrant отзывает grant. Отзыв вступает в силу при следующей оценке; сам по себе не зависит от
// подтверждения аудита (отозванный grant не пригоден в любом случае).
func (s *Service) RevokeGrant(ctx context.Context, req RevokeGrantRequest) (Grant, error) {
	if !validID(req.MutationID) {
		return Grant{}, invalid("MutationID")
	}
	if !validID(req.ID) {
		return Grant{}, invalid("ID")
	}
	if req.ExpectedRevision == 0 {
		return Grant{}, invalid("ExpectedRevision")
	}
	cur, err := s.grants.GetGrant(ctx, req.ID)
	if err != nil {
		return Grant{}, normalize(err)
	}
	if err := s.authorizeGrant(ctx, req.Actor, cur.Principal, AdminGrantRevoke); err != nil {
		return Grant{}, err
	}
	if cur.LastMutationID == req.MutationID {
		if cur.State == GrantRevoked && cur.Revision == req.ExpectedRevision+1 {
			return s.replayed(ctx, cur.Principal.OrgID, cur.LastAuditEventID, cur.Revision, cur)
		}
		return Grant{}, conflict("mutation id reused")
	}
	p := cur.Principal
	var out Grant
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "grant.revoke", resourceType: "grant", resourceID: cur.ID,
		principal: p, orgID: p.OrgID, teamID: p.TeamID, mutationID: req.MutationID, expectedRev: req.ExpectedRevision,
		digest: requestDigest("grant.revoke", cur.ID, fmt.Sprint(req.ExpectedRevision), string(req.Actor.Kind),
			req.Actor.ID, req.MutationID),
	}, func(eventID string) (uint64, string, error) {
		got, err := s.grants.RevokeGrant(ctx, cur.ID, req.ExpectedRevision, req.MutationID, eventID)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

// ---------- use cases: delegation ----------

// CreateDelegation выдаёт Cross-Team Delegation от Team-владельца ServicePrincipal'у той же Organization.
// Полномочия проверяет AuthorizeDelegationAdmin для OwnerTeamID: Organization Admin её не выдаёт.
func (s *Service) CreateDelegation(ctx context.Context, req CreateDelegationRequest) (Delegation, error) {
	if !validID(req.MutationID) {
		return Delegation{}, invalid("MutationID")
	}
	sp := req.ServicePrincipal
	d := Delegation{
		ID: derivedID("dlg-", req.OrgID, req.OwnerTeamID, sp.ID, req.MutationID), OrgID: req.OrgID,
		OwnerTeamID: req.OwnerTeamID, ServicePrincipal: sp, Actions: req.Actions, Targets: req.Targets,
		Constraints: req.Constraints, State: DelegationActive, ExpiresAt: req.ExpiresAt, CreatedBy: req.Actor,
		LastMutationID: req.MutationID, LastAuditEventID: pendingEventID,
	}.Canonical()
	if err := d.validate(false); err != nil {
		return Delegation{}, err
	}
	if err := s.authorizeDelegation(ctx, req.Actor, d.OwnerTeamID, AdminDelegationCreate); err != nil {
		return Delegation{}, err
	}

	cur, err := s.delegs.GetDelegation(ctx, d.ID)
	switch {
	case err == nil:
		if cur.Revision == 1 && cur.LastMutationID == req.MutationID && cur.CreatedBy == req.Actor && sameDelegationContent(cur, d) {
			return s.replayedDelegation(ctx, cur)
		}
		return Delegation{}, conflict("delegation exists")
	case !errors.Is(err, ErrNotFound):
		return Delegation{}, normalize(err)
	}

	if err := s.principalUsable(ctx, sp); err != nil {
		return Delegation{}, err
	}
	if err := s.checkFuture(d.ExpiresAt); err != nil {
		return Delegation{}, err
	}
	if err := s.checkDelegationScope(ctx, d.OrgID, d.OwnerTeamID, d.Targets); err != nil {
		return Delegation{}, err
	}

	var out Delegation
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "delegation.create", resourceType: "delegation", resourceID: d.ID,
		principal: sp, orgID: d.OrgID, teamID: d.OwnerTeamID, mutationID: req.MutationID, expectedRev: 0,
		digest: requestDigest(append([]string{"delegation.create", d.ID, d.OrgID, d.OwnerTeamID, string(sp.Kind), sp.ID,
			string(req.Actor.Kind), req.Actor.ID, req.MutationID}, contentParts(d.Actions, d.Targets, d.Constraints, d.ExpiresAt)...)...),
	}, func(eventID string) (uint64, string, error) {
		in := d
		in.LastAuditEventID = eventID
		got, err := s.delegs.CreateDelegation(ctx, in)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

// UpdateDelegation заменяет изменяемые поля delegation; состояние сохраняется.
func (s *Service) UpdateDelegation(ctx context.Context, req UpdateDelegationRequest) (Delegation, error) {
	if !validID(req.MutationID) {
		return Delegation{}, invalid("MutationID")
	}
	if !validID(req.ID) {
		return Delegation{}, invalid("ID")
	}
	if req.ExpectedRevision == 0 {
		return Delegation{}, invalid("ExpectedRevision")
	}
	cur, err := s.delegs.GetDelegation(ctx, req.ID)
	if err != nil {
		return Delegation{}, normalize(err)
	}
	if err := s.authorizeDelegation(ctx, req.Actor, cur.OwnerTeamID, AdminDelegationUpdate); err != nil {
		return Delegation{}, err
	}
	next := cur
	next.Actions, next.Targets, next.Constraints, next.ExpiresAt = req.Actions, req.Targets, req.Constraints, req.ExpiresAt
	next.Revision = 0
	next.LastMutationID, next.LastAuditEventID = req.MutationID, pendingEventID
	next = next.Canonical()
	shape := next
	shape.State = DelegationActive
	if err := shape.validate(false); err != nil {
		return Delegation{}, err
	}
	if cur.LastMutationID == req.MutationID {
		if cur.State != DelegationRevoked && cur.Revision == req.ExpectedRevision+1 && sameDelegationContent(cur, next) {
			return s.replayedDelegation(ctx, cur)
		}
		return Delegation{}, conflict("mutation id reused")
	}
	if cur.State == DelegationRevoked {
		return Delegation{}, fmt.Errorf("%w: %s", ErrRevoked, "delegation")
	}
	if err := s.checkFuture(next.ExpiresAt); err != nil {
		return Delegation{}, err
	}
	if err := s.checkDelegationScope(ctx, cur.OrgID, cur.OwnerTeamID, next.Targets); err != nil {
		return Delegation{}, err
	}
	return s.mutateDelegation(ctx, cur, next, req.Actor, "delegation.update", req.ExpectedRevision, req.MutationID)
}

// SuspendDelegation: active → suspended. Приостановленная delegation ничего не разрешает.
func (s *Service) SuspendDelegation(ctx context.Context, req DelegationTransitionRequest) (Delegation, error) {
	return s.transitionDelegation(ctx, req, DelegationSuspended, AdminDelegationSuspend, "delegation.suspend")
}

// ResumeDelegation: suspended → active.
func (s *Service) ResumeDelegation(ctx context.Context, req DelegationTransitionRequest) (Delegation, error) {
	return s.transitionDelegation(ctx, req, DelegationActive, AdminDelegationUpdate, "delegation.resume")
}

func (s *Service) transitionDelegation(ctx context.Context, req DelegationTransitionRequest, to DelegationState,
	action AdminAction, auditAction string) (Delegation, error) {
	if !validID(req.MutationID) {
		return Delegation{}, invalid("MutationID")
	}
	if !validID(req.ID) {
		return Delegation{}, invalid("ID")
	}
	if req.ExpectedRevision == 0 {
		return Delegation{}, invalid("ExpectedRevision")
	}
	cur, err := s.delegs.GetDelegation(ctx, req.ID)
	if err != nil {
		return Delegation{}, normalize(err)
	}
	if err := s.authorizeDelegation(ctx, req.Actor, cur.OwnerTeamID, action); err != nil {
		return Delegation{}, err
	}
	if cur.LastMutationID == req.MutationID {
		if cur.State == to && cur.Revision == req.ExpectedRevision+1 {
			return s.replayedDelegation(ctx, cur)
		}
		return Delegation{}, conflict("mutation id reused")
	}
	if err := validDelegationTransition(cur.State, to); err != nil {
		return Delegation{}, err
	}
	if cur.State == to {
		return Delegation{}, invalid("Delegation.State transition")
	}
	next := cur
	next.State, next.Revision = to, 0
	next.LastMutationID, next.LastAuditEventID = req.MutationID, pendingEventID
	return s.mutateDelegation(ctx, cur, next.Canonical(), req.Actor, auditAction, req.ExpectedRevision, req.MutationID)
}

func (s *Service) mutateDelegation(ctx context.Context, cur, next Delegation, actor identity.ActorRef,
	auditAction string, expected uint64, mutationID string) (Delegation, error) {
	var out Delegation
	err := s.protect(ctx, protected{
		actor: actor, action: auditAction, resourceType: "delegation", resourceID: cur.ID,
		principal: cur.ServicePrincipal, orgID: cur.OrgID, teamID: cur.OwnerTeamID, mutationID: mutationID, expectedRev: expected,
		digest: requestDigest(append([]string{auditAction, cur.ID, fmt.Sprint(expected), string(next.State),
			string(actor.Kind), actor.ID, mutationID}, contentParts(next.Actions, next.Targets, next.Constraints, next.ExpiresAt)...)...),
	}, func(eventID string) (uint64, string, error) {
		in := next
		in.LastAuditEventID = eventID
		got, err := s.delegs.UpdateDelegation(ctx, in, expected)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

// RevokeDelegation отзывает delegation (терминально).
func (s *Service) RevokeDelegation(ctx context.Context, req DelegationTransitionRequest) (Delegation, error) {
	if !validID(req.MutationID) {
		return Delegation{}, invalid("MutationID")
	}
	if !validID(req.ID) {
		return Delegation{}, invalid("ID")
	}
	if req.ExpectedRevision == 0 {
		return Delegation{}, invalid("ExpectedRevision")
	}
	cur, err := s.delegs.GetDelegation(ctx, req.ID)
	if err != nil {
		return Delegation{}, normalize(err)
	}
	if err := s.authorizeDelegation(ctx, req.Actor, cur.OwnerTeamID, AdminDelegationRevoke); err != nil {
		return Delegation{}, err
	}
	if cur.LastMutationID == req.MutationID {
		if cur.State == DelegationRevoked && cur.Revision == req.ExpectedRevision+1 {
			return s.replayedDelegation(ctx, cur)
		}
		return Delegation{}, conflict("mutation id reused")
	}
	var out Delegation
	err = s.protect(ctx, protected{
		actor: req.Actor, action: "delegation.revoke", resourceType: "delegation", resourceID: cur.ID,
		principal: cur.ServicePrincipal, orgID: cur.OrgID, teamID: cur.OwnerTeamID, mutationID: req.MutationID,
		expectedRev: req.ExpectedRevision,
		digest: requestDigest("delegation.revoke", cur.ID, fmt.Sprint(req.ExpectedRevision), string(req.Actor.Kind),
			req.Actor.ID, req.MutationID),
	}, func(eventID string) (uint64, string, error) {
		got, err := s.delegs.RevokeDelegation(ctx, cur.ID, req.ExpectedRevision, req.MutationID, eventID)
		if err != nil {
			return 0, "", err
		}
		out = got
		return got.Revision, got.LastAuditEventID, nil
	})
	if err != nil {
		return Delegation{}, err
	}
	return out, nil
}

// ---------- чтение (trusted server-side; права на просмотр проверяет транспортный слой) ----------

// GetGrant возвращает grant в любом состоянии как метаданные. Для решения «можно ли использовать» служит Evaluator.
func (s *Service) GetGrant(ctx context.Context, id string) (Grant, error) {
	g, err := s.grants.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, normalize(err)
	}
	return g, nil
}

// ListGrants возвращает grants ровно этого principal.
func (s *Service) ListGrants(ctx context.Context, principal identity.PrincipalRef) ([]Grant, error) {
	l, err := s.grants.ListGrants(ctx, principal)
	if err != nil {
		return nil, normalize(err)
	}
	return l, nil
}

// GetDelegation возвращает delegation в любом состоянии как метаданные.
func (s *Service) GetDelegation(ctx context.Context, id string) (Delegation, error) {
	d, err := s.delegs.GetDelegation(ctx, id)
	if err != nil {
		return Delegation{}, normalize(err)
	}
	return d, nil
}

// ListDelegations возвращает delegations сервиса от ровно этой Team.
func (s *Service) ListDelegations(ctx context.Context, service identity.PrincipalRef, ownerTeamID string) ([]Delegation, error) {
	l, err := s.delegs.ListDelegations(ctx, service, ownerTeamID)
	if err != nil {
		return nil, normalize(err)
	}
	return l, nil
}

// ---------- проверки ----------

func (s *Service) authorizeActor(actor identity.ActorRef) error {
	if err := actor.Validate(); err != nil {
		return invalid("Actor")
	}
	if actor.Kind != identity.ActorUser {
		return deny0("administrative actions require a user actor")
	}
	return nil
}

func deny0(reason string) error { return fmt.Errorf("%w: %s", ErrForbidden, reason) }

func (s *Service) mapAuthz(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrForbidden), errors.Is(err, identity.ErrForbidden):
		return deny0("administrative action is not permitted")
	default:
		return unavailable("admin authorizer")
	}
}

func (s *Service) authorizeGrant(ctx context.Context, actor identity.ActorRef, p identity.PrincipalRef, action AdminAction) error {
	if err := s.authorizeActor(actor); err != nil {
		return err
	}
	return s.mapAuthz(s.authz.AuthorizeGrantAdmin(ctx, actor, p, action))
}

// authorizeDelegation всегда вызывает AuthorizeDelegationAdmin для Team-владельца и никогда не заменяет его
// проверкой grant-администрирования или ролью Organization Admin.
func (s *Service) authorizeDelegation(ctx context.Context, actor identity.ActorRef, ownerTeamID string, action AdminAction) error {
	if err := s.authorizeActor(actor); err != nil {
		return err
	}
	return s.mapAuthz(s.authz.AuthorizeDelegationAdmin(ctx, actor, ownerTeamID, action))
}

// principalUsable: principal существует и не отозван (приостановленному grant выдать можно: он останется
// непригодным до возобновления).
func (s *Service) principalUsable(ctx context.Context, p identity.PrincipalRef) error {
	st, err := s.principals.GetPrincipalState(ctx, p)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, identity.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return unavailable("identity state")
	}
	switch st {
	case identity.IdentityActive, identity.IdentitySuspended:
		return nil
	case identity.IdentityRevoked:
		return fmt.Errorf("%w: %s", ErrRevoked, "principal")
	}
	return unavailable("identity state")
}

// checkFuture: выдаваемый срок обязан быть в будущем по часам Service.
func (s *Service) checkFuture(exp time.Time) error {
	if !exp.After(s.clock.Now()) {
		return invalid("ExpiresAt")
	}
	return nil
}

// checkGrantScope сверяет цели grant с доверенным каталогом: Team-принципал — только кластеры своей Team,
// Service-принципал — только кластеры своей Organization. Чужие и неизвестные кластеры неотличимы (ErrInvalid).
func (s *Service) checkGrantScope(ctx context.Context, p identity.PrincipalRef, t TargetScope) error {
	teamID := ""
	if p.Kind == identity.PrincipalTeam {
		teamID = p.TeamID
	}
	return s.checkClusters(ctx, p.OrgID, teamID, t)
}

func (s *Service) checkDelegationScope(ctx context.Context, orgID, ownerTeamID string, t TargetScope) error {
	return s.checkClusters(ctx, orgID, ownerTeamID, t)
}

func (s *Service) checkClusters(ctx context.Context, orgID, teamID string, t TargetScope) error {
	for _, c := range t.clusterIDs() {
		f, err := s.owners.ResolveTargetFacts(ctx, TargetRef{ClusterID: c})
		switch {
		case errors.Is(err, ErrNotFound):
			return invalid("Targets")
		case err != nil:
			return unavailable("ownership resolver")
		}
		if !validID(f.OrgID) || !validID(f.TeamID) {
			return unavailable("ownership resolver") // некорректный результат каталога — отказ, не «любая»
		}
		if f.OrgID != orgID || (teamID != "" && f.TeamID != teamID) {
			return invalid("Targets")
		}
	}
	return nil
}

// ---------- протокол критичной мутации ----------

type protected struct {
	actor        identity.ActorRef
	action       string
	resourceType string
	resourceID   string
	principal    identity.PrincipalRef
	orgID        string
	teamID       string
	mutationID   string
	digest       string
	expectedRev  uint64
}

// protect исполняет шаги 3–6. apply вызывается не более одного раза и только после durable AppendIntent;
// возвращает revision результата и LastAuditEventID полученной записи.
func (s *Service) protect(ctx context.Context, p protected, apply func(eventID string) (uint64, string, error)) error {
	eventID, err := s.newEventID()
	if err != nil || !validID(eventID) {
		return unavailable("event id generation")
	}
	intent := audit.CriticalIntent{
		EventID: eventID, MutationID: p.mutationID, RequestDigest: p.digest,
		ActorKind: string(p.actor.Kind), ActorID: p.actor.ID,
		PrincipalKind: string(p.principal.Kind), PrincipalID: p.principal.ID,
		OrgID: p.orgID, TeamID: p.teamID, Action: p.action, ResourceType: p.resourceType, ResourceID: p.resourceID,
		ExpectedRevision: p.expectedRev, OccurredAt: s.clock.Now(),
	}
	if err := s.rec.AppendIntent(ctx, intent); err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, "critical audit intent") // Backend не вызывался
	}

	octx := context.WithoutCancel(ctx) // исход фиксируется даже если вызывающий отменил ctx после мутации
	rev, lastEvent, mutErr := apply(eventID)
	if mutErr != nil {
		mutErr = normalize(mutErr)
		if aerr := s.rec.AppendOutcome(octx, failureOutcome(eventID, mutErr, s.clock.Now())); aerr != nil {
			return errors.Join(fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr), mutErr)
		}
		return mutErr
	}
	if lastEvent != eventID {
		// Идентичный запрос успел примениться параллельно: Backend вернул его запись. Эта попытка состояние
		// не меняла; outcome её события пишет только владелец lastEvent.
		out := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalFailed, ResourceRevision: rev,
			ReasonCode: "replayed", CompletedAt: s.clock.Now()}
		if aerr := s.rec.AppendOutcome(octx, out); aerr != nil {
			return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr)
		}
		existing, getErr := s.rec.GetOutcome(octx, p.orgID, lastEvent)
		switch {
		case getErr == nil && existing.Result == audit.CriticalSucceeded:
			return nil
		case errors.Is(getErr, audit.ErrCriticalNotFound):
			return fmt.Errorf("%w: %s", audit.ErrOutcomeUnknown, "concurrent mutation is not yet audited")
		case getErr != nil:
			return unavailable("critical audit")
		default:
			return fmt.Errorf("%w: %s", audit.ErrOutcomeUnknown, "concurrent mutation has a non-succeeded outcome")
		}
	}
	out := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalSucceeded, ResourceRevision: rev, CompletedAt: s.clock.Now()}
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
// LastAuditEventID этой попытки, значит мутация выполнена. Нет outcome — пишется succeeded; есть succeeded —
// ничего; иной outcome противоречит состоянию записи → ErrOutcomeUnknown (нужна ручная сверка).
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
		out := audit.CriticalOutcome{EventID: eventID, Result: audit.CriticalSucceeded, ResourceRevision: revision, CompletedAt: s.clock.Now()}
		if aerr := s.rec.AppendOutcome(ctx, out); aerr != nil {
			return fmt.Errorf("%w: %w", audit.ErrOutcomeUnknown, aerr)
		}
		return nil
	default:
		return unavailable("critical audit")
	}
}

func (s *Service) replayed(ctx context.Context, orgID, eventID string, rev uint64, cur Grant) (Grant, error) {
	if err := s.confirm(ctx, orgID, eventID, rev); err != nil {
		return Grant{}, err
	}
	return cur, nil
}

func (s *Service) replayedDelegation(ctx context.Context, cur Delegation) (Delegation, error) {
	if err := s.confirm(ctx, cur.OrgID, cur.LastAuditEventID, cur.Revision); err != nil {
		return Delegation{}, err
	}
	return cur, nil
}

// normalize приводит ошибку зависимости к sentinel'ам пакета: известные проходят как есть, прочие означают
// недоступность (fail closed). Текст причины не сохраняется.
func normalize(err error) error {
	for _, target := range []error{ErrNotFound, ErrConflict, ErrInvalid, ErrForbidden, ErrRevoked, ErrUnavailable} {
		if errors.Is(err, target) {
			return err
		}
	}
	return unavailable("dependency")
}

// ---------- digest ----------

func derivedID(prefix string, parts ...string) string {
	sum := sha256.New()
	writeParts(sum, parts)
	return prefix + hex.EncodeToString(sum.Sum(nil)[:16])
}

func requestDigest(parts ...string) string {
	sum := sha256.New()
	writeParts(sum, parts)
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

type byteWriter interface{ Write(p []byte) (int, error) }

func writeParts(w byteWriter, parts []string) {
	var n [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		w.Write(n[:])
		w.Write([]byte(p))
	}
}

// contentParts — каноническое однозначное представление изменяемого содержимого для digest (списки с
// количеством; все части — с длинами). Значения в audit не пишутся — только их sha256.
func contentParts(actions []OperationAction, t TargetScope, c ConstraintSet, exp time.Time) []string {
	a := canonicalActions(actions)
	t = t.canonical()
	c = c.canonical()
	n := func(i int) string { return strconv.Itoa(i) }
	parts := []string{"actions", n(len(a))}
	for _, x := range a {
		parts = append(parts, x.OperationID, x.ActionID)
	}
	parts = append(parts, "scope", t.OrgID, "teams", n(len(t.TeamIDs)))
	parts = append(parts, t.TeamIDs...)
	parts = append(parts, "clusters", n(len(t.ClusterIDs)))
	parts = append(parts, t.ClusterIDs...)
	parts = append(parts, "namespaces", n(len(t.Namespaces)))
	for _, x := range t.Namespaces {
		parts = append(parts, x.ClusterID, x.Namespace)
	}
	parts = append(parts, "objects", n(len(t.Objects)))
	for _, o := range t.Objects {
		parts = append(parts, o.ClusterID, o.Group, o.Version, o.Resource, o.Namespace, o.Name, o.UID)
	}
	parts = append(parts, "envs", n(len(c.AllowedEnvironments)))
	parts = append(parts, c.AllowedEnvironments...)
	nb := "-"
	if c.NotBefore != nil {
		nb = c.NotBefore.Format(canonicalTimeFmt)
	}
	parts = append(parts, "destructive", strconv.FormatBool(c.DenyDestructive), "not_before", nb,
		"expires", canon(exp).Format(canonicalTimeFmt))
	return parts
}
