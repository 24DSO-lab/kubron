// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

// Evaluator (Contract Freeze §6.3). Принципы:
//
//   - fail closed: нет grant/delegation, истёкшее/отозванное состояние, неизвестный владелец, malformed scope,
//     неподдерживаемый constraint, неподтверждённый аудит или недоступная зависимость — отказ;
//   - grants объединяются по union, constraints внутри одного grant — по AND; идентичность, тенантность,
//     владение и обязательная delegation только сужают результат (пересечение);
//   - никакого кеша положительных решений: каждый вызов заново читает состояние из доверенных источников,
//     а перед выдачей решения проверяет, что принципал и использованные записи не изменились (TOCTOU);
//     это проверка снимка перед возвратом, а не атомарная гарантия до Plan Commit;
//   - не раскрывает метаданные чужих целей: цель вне видимости principal'а неотличима от несуществующей;
//   - не загружает credentials, не строит Kubernetes-клиентов и не вызывает Kubernetes API.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/identity"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Deps — зависимости Evaluator'а. Все обязательны: «разрешающих» заглушек нет.
type Deps struct {
	Grants      GrantReader
	Delegations DelegationReader
	Owners      OwnershipResolver
	Principals  IdentityStateReader
	Actions     ActionFactsResolver
	Outcomes    OutcomeReader
	Clock       Clock
}

// GrantEvaluator — реализация Evaluator. Не хранит состояния между вызовами и потокобезопасен, если
// безопасны его зависимости.
type GrantEvaluator struct{ d Deps }

var _ Evaluator = (*GrantEvaluator)(nil)

// NewEvaluator собирает Evaluator; любая nil-зависимость — ошибка (fail closed).
func NewEvaluator(d Deps) (*GrantEvaluator, error) {
	switch {
	case d.Grants == nil:
		return nil, invalid("Deps.Grants")
	case d.Delegations == nil:
		return nil, invalid("Deps.Delegations")
	case d.Owners == nil:
		return nil, invalid("Deps.Owners")
	case d.Principals == nil:
		return nil, invalid("Deps.Principals")
	case d.Actions == nil:
		return nil, invalid("Deps.Actions")
	case d.Outcomes == nil:
		return nil, invalid("Deps.Outcomes")
	case d.Clock == nil:
		return nil, invalid("Deps.Clock")
	}
	return &GrantEvaluator{d: d}, nil
}

// verdict — итог проверки одной записи; порядок значений = «близость к допуску» (для выбора причины отказа
// независимо от порядка записей в хранилище).
type verdict int

const (
	vNotApplicable verdict = iota
	vInvalid
	vRevoked
	vSuspended
	vExpired
	vConstraintUnsupported
	vConstraintNotMet
	vAuditUnconfirmed
	vQualifies
)

// ruleView — общий вид grant/delegation для единой проверки.
type ruleView struct {
	actions       []OperationAction
	targets       TargetScope
	constraints   ConstraintSet
	expiresAt     time.Time
	revoked       bool
	suspended     bool
	validErr      error
	constraintErr error
}

type evalCtx struct {
	now    time.Time
	op     string
	act    string
	target TargetRef
	scope  tenant.Scope // доверенные OrgID/TeamID владельца + ClusterID цели
	facts  evalFacts
}

// judge — проверка записи без обращения к аудиту. Применимость (пара действия + цель) определяется по
// сырым данным ДО проверки валидности: повреждённая, но неприменимая запись не загрязняет причину отказа.
func judge(r ruleView, ec evalCtx) verdict {
	if !hasAction(r.actions, ec.op, ec.act) || !r.targets.covers(ec.scope.OrgID, ec.scope.TeamID, ec.target) {
		return vNotApplicable
	}
	if r.constraintErr != nil {
		return vConstraintUnsupported
	}
	if r.validErr != nil {
		return vInvalid
	}
	switch {
	case r.revoked:
		return vRevoked
	case r.suspended:
		return vSuspended
	case !ec.now.Before(r.expiresAt): // ExpiresAt исключителен
		return vExpired
	case !r.constraints.satisfied(ec.facts):
		return vConstraintNotMet
	}
	return vQualifies
}

func viewGrant(g Grant) ruleView {
	return ruleView{
		actions: g.Actions, targets: g.Targets, constraints: g.Constraints, expiresAt: g.ExpiresAt,
		revoked: g.State == GrantRevoked, validErr: g.validate(true), constraintErr: g.Constraints.Validate(),
	}
}

func viewDelegation(d Delegation) ruleView {
	return ruleView{
		actions: d.Actions, targets: d.Targets, constraints: d.Constraints, expiresAt: d.ExpiresAt,
		revoked: d.State == DelegationRevoked, suspended: d.State == DelegationSuspended,
		validErr: d.validate(true), constraintErr: d.Constraints.Validate(),
	}
}

func grantReason(v verdict) string {
	switch v {
	case vInvalid:
		return ReasonGrantInvalid
	case vRevoked:
		return ReasonGrantRevoked
	case vExpired:
		return ReasonGrantExpired
	case vConstraintUnsupported:
		return ReasonGrantConstraintUnsupported
	case vConstraintNotMet:
		return ReasonGrantConstraintNotMet
	case vAuditUnconfirmed:
		return ReasonGrantAuditUnconfirmed
	}
	return ReasonGrantRequired
}

func delegationReason(v verdict) string {
	switch v {
	case vInvalid:
		return ReasonDelegationInvalid
	case vRevoked:
		return ReasonDelegationRevoked
	case vSuspended:
		return ReasonDelegationSuspended
	case vExpired:
		return ReasonDelegationExpired
	case vConstraintUnsupported:
		return ReasonDelegationConstraintUnsupported
	case vConstraintNotMet:
		return ReasonDelegationConstraintNotMet
	case vAuditUnconfirmed:
		return ReasonDelegationAuditUnconfirmed
	}
	return ReasonDelegationRequired
}

func deny(reason string) GrantDecision { return GrantDecision{ReasonCode: reason} }

// unavailable — ошибка недоступности зависимости. Текст причины зависимости намеренно не включается:
// он может содержать метаданные чужих целей.
func unavailable(what string) error { return fmt.Errorf("%w: %s", ErrUnavailable, what) }

// EvaluateKubronGrant оценивает кортеж. Детерминированные отказы возвращаются как GrantDecision{Granted:false}
// с nil-ошибкой; недоступность зависимости — GrantDecision{Granted:false, ReasonCode:dependency_unavailable}
// вместе с ошибкой, для которой errors.Is(err, ErrUnavailable). Вызывающий обязан считать отказом всё, кроме
// (Granted==true, err==nil).
func (e *GrantEvaluator) EvaluateKubronGrant(ctx context.Context, req GrantRequest) (GrantDecision, error) {
	dec, err := e.evaluate(ctx, req)
	if err != nil {
		return deny(ReasonDependencyUnavailable), err
	}
	return dec, nil
}

func (e *GrantEvaluator) evaluate(ctx context.Context, req GrantRequest) (GrantDecision, error) {
	if err := ctx.Err(); err != nil {
		return GrantDecision{}, unavailable("context")
	}
	p := req.Principal
	if p.Validate() != nil {
		return deny(ReasonRequestInvalid), nil
	}
	if !p.IsTechnical() {
		return deny(ReasonPrincipalUnsupported), nil // user principals живут в app-RBAC, не в technical grants
	}
	if !validID(req.OperationID) || !validID(req.ActionID) || req.Target.Validate() != nil {
		return deny(ReasonRequestInvalid), nil
	}
	now := e.d.Clock.Now()
	if now.IsZero() {
		return GrantDecision{}, unavailable("clock")
	}
	now = now.UTC()

	// 1. Principal: существует и active (адаптер подтверждает и критичный аудит identity).
	if dec, err := e.checkPrincipal(ctx, p); err != nil || dec != nil {
		if err != nil {
			return GrantDecision{}, err
		}
		return *dec, nil
	}

	// 2. Доверенное владение целью. Всё, что не доказывает «цель в моей Organization/Team», неотличимо от
	// отсутствующей цели.
	facts, err := e.d.Owners.ResolveTargetFacts(ctx, req.Target)
	switch {
	case errors.Is(err, ErrNotFound):
		return deny(ReasonTargetNotAccessible), nil
	case err != nil:
		return GrantDecision{}, unavailable("ownership resolver")
	}
	scope := tenant.Scope{OrgID: facts.OrgID, TeamID: facts.TeamID, ClusterID: req.Target.ClusterID}
	if !scope.Valid() || !validID(scope.OrgID) || !validID(scope.TeamID) ||
		(facts.Environment != "" && !validID(facts.Environment)) {
		return deny(ReasonTargetNotAccessible), nil // пустое/некорректное владение — отказ, не «любая»
	}
	if scope.OrgID != p.OrgID {
		return deny(ReasonTargetNotAccessible), nil
	}
	if p.Kind == identity.PrincipalTeam && scope.TeamID != p.TeamID {
		return deny(ReasonTargetNotAccessible), nil // TeamPrincipal строго внутри своей Team; delegation не расширяет
	}

	// 3. Факты действия из доверенного каталога.
	af, err := e.d.Actions.ResolveActionFacts(ctx, req.OperationID, req.ActionID)
	switch {
	case errors.Is(err, ErrNotFound):
		return deny(ReasonActionUnknown), nil
	case err != nil:
		return GrantDecision{}, unavailable("action catalogue")
	}

	ec := evalCtx{
		now: now, op: req.OperationID, act: req.ActionID, target: req.Target, scope: scope,
		facts: evalFacts{now: now, environment: facts.Environment, destructive: af.Destructive},
	}

	// 4. ServicePrincipal: обязательная delegation от Team-владельца цели.
	var del *Delegation
	if p.Kind == identity.PrincipalService {
		d, reason, err := e.findDelegation(ctx, p, ec)
		if err != nil {
			return GrantDecision{}, err
		}
		if d == nil {
			return deny(reason), nil
		}
		del = d
	}

	// 5. Grants: union применимых; constraints внутри каждого — AND.
	grants, reason, err := e.qualifyingGrants(ctx, p, ec)
	if err != nil {
		return GrantDecision{}, err
	}
	if len(grants) == 0 {
		return deny(reason), nil
	}

	// 6. TOCTOU: перед выдачей убеждаемся, что principal и использованные записи не изменились.
	// Для delegation наружу возвращается только объект, заново прочитанный и подтверждённый ниже.
	checkedDel, dec, err := e.recheck(ctx, p, grants, del)
	if err != nil {
		return GrantDecision{}, err
	}
	if dec != nil {
		return *dec, nil
	}
	del = checkedDel

	out := GrantDecision{Granted: true, ReasonCode: ReasonGranted}
	for _, g := range grants {
		out.GrantIDs = append(out.GrantIDs, g.ID)
		out.GrantRevisions = append(out.GrantRevisions, g.Revision)
	}
	if del != nil {
		out.DelegationID = del.ID
		out.DelegationRevision = del.Revision
	}
	return out, nil
}

// checkPrincipal: nil-решение и nil-ошибка — principal active.
func (e *GrantEvaluator) checkPrincipal(ctx context.Context, p identity.PrincipalRef) (*GrantDecision, error) {
	st, err := e.d.Principals.GetPrincipalState(ctx, p)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, identity.ErrNotFound):
		d := deny(ReasonPrincipalUnknown)
		return &d, nil
	case err != nil:
		return nil, unavailable("identity state")
	}
	if st != identity.IdentityActive {
		d := deny(ReasonPrincipalNotActive)
		return &d, nil
	}
	return nil, nil
}

// confirmed — «пригодность» записи: подтверждённый succeeded outcome её LastAuditEventID. Отсутствующий,
// failed или unknown — false; недоступность аудита — ошибка (fail closed).
func (e *GrantEvaluator) confirmed(ctx context.Context, orgID, eventID string) (bool, error) {
	out, err := e.d.Outcomes.GetOutcome(ctx, orgID, eventID)
	switch {
	case err == nil:
		return out.EventID == eventID && out.Result == audit.CriticalSucceeded, nil
	case errors.Is(err, audit.ErrCriticalNotFound):
		return false, nil
	default:
		return false, unavailable("critical audit")
	}
}

// findDelegation выбирает действующую delegation от Team-владельца цели для ровно этого ServicePrincipal.
// При нескольких подходящих возвращается запись с наименьшим ID. nil + причина — отказ.
func (e *GrantEvaluator) findDelegation(ctx context.Context, p identity.PrincipalRef, ec evalCtx) (*Delegation, string, error) {
	list, err := e.d.Delegations.ListDelegations(ctx, p, ec.scope.TeamID)
	if err != nil {
		return nil, "", unavailable("delegation store")
	}
	best := vNotApplicable
	var qualifying []Delegation
	for _, d := range list {
		// Защита от backend'а, вернувшего чужие записи: delegation обязана быть ровно этого сервиса от ровно
		// этой Team в этой Organization.
		if d.ServicePrincipal != p || d.OrgID != p.OrgID || d.OwnerTeamID != ec.scope.TeamID {
			continue
		}
		v := judge(viewDelegation(d), ec)
		if v == vQualifies {
			ok, err := e.confirmed(ctx, d.OrgID, d.LastAuditEventID)
			if err != nil {
				return nil, "", err
			}
			if ok {
				qualifying = append(qualifying, d)
				continue
			}
			v = vAuditUnconfirmed
		}
		best = max(best, v)
	}
	if len(qualifying) == 0 {
		return nil, delegationReason(best), nil
	}
	slices.SortFunc(qualifying, func(a, b Delegation) int { return strings.Compare(a.ID, b.ID) })
	d := qualifying[0]
	return &d, "", nil
}

// qualifyingGrants возвращает все grants, разрешающие кортеж, отсортированные по ID. Порядок записей в
// хранилище на результат не влияет.
func (e *GrantEvaluator) qualifyingGrants(ctx context.Context, p identity.PrincipalRef, ec evalCtx) ([]Grant, string, error) {
	list, err := e.d.Grants.ListGrants(ctx, p)
	if err != nil {
		return nil, "", unavailable("grant store")
	}
	best := vNotApplicable
	var qualifying []Grant
	for _, g := range list {
		if g.Principal != p {
			continue // чужая запись от некорректного backend'а не участвует
		}
		v := judge(viewGrant(g), ec)
		if v == vQualifies {
			ok, err := e.confirmed(ctx, g.Principal.OrgID, g.LastAuditEventID)
			if err != nil {
				return nil, "", err
			}
			if ok {
				qualifying = append(qualifying, g)
				continue
			}
			v = vAuditUnconfirmed
		}
		best = max(best, v)
	}
	slices.SortFunc(qualifying, func(a, b Grant) int { return strings.Compare(a.ID, b.ID) })
	return qualifying, grantReason(best), nil
}

// recheck повторно читает principal и использованные записи по ID: изменившаяся revision, отзыв,
// приостановка, исчезновение или истечение за время оценки — отказ (stale_revision и т.п.).
func (e *GrantEvaluator) recheck(ctx context.Context, p identity.PrincipalRef, grants []Grant, del *Delegation) (*Delegation, *GrantDecision, error) {
	if dec, err := e.checkPrincipal(ctx, p); err != nil || dec != nil {
		return nil, dec, err
	}
	now := e.d.Clock.Now().UTC()
	stale := func(reason string) (*Delegation, *GrantDecision, error) {
		d := deny(reason)
		return nil, &d, nil
	}
	var checkedDel *Delegation
	if del != nil {
		cur, err := e.d.Delegations.GetDelegation(ctx, del.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			return stale(ReasonStaleRevision)
		case err != nil:
			return nil, nil, unavailable("delegation store")
		}
		// ID, principal, owner Team and revision must describe the exact record selected from the list.
		// Never copy the revision from the stale list result into GrantDecision.
		if cur.ID != del.ID || cur.ServicePrincipal != p || cur.OrgID != p.OrgID ||
			cur.OwnerTeamID != del.OwnerTeamID || cur.Revision != del.Revision ||
			cur.LastMutationID != del.LastMutationID || cur.LastAuditEventID != del.LastAuditEventID {
			return stale(ReasonStaleRevision)
		}
		if cur.State != DelegationActive {
			switch cur.State {
			case DelegationRevoked:
				return stale(ReasonDelegationRevoked)
			case DelegationSuspended:
				return stale(ReasonDelegationSuspended)
			default:
				return stale(ReasonDelegationInvalid)
			}
		}
		if cur.validate(true) != nil {
			return stale(ReasonDelegationInvalid)
		}
		if !now.Before(cur.ExpiresAt) {
			return stale(ReasonDelegationExpired)
		}
		// Critical Audit is checked again for the exact object returned by GetDelegation.
		// A successful list-time audit check alone is not sufficient for a positive decision.
		ok, err := e.confirmed(ctx, cur.OrgID, cur.LastAuditEventID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return stale(ReasonDelegationAuditUnconfirmed)
		}
		cur = cur.Canonical()
		checkedDel = &cur
	}
	for _, g := range grants {
		cur, err := e.d.Grants.GetGrant(ctx, g.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			return stale(ReasonStaleRevision)
		case err != nil:
			return nil, nil, unavailable("grant store")
		}
		if cur.Revision != g.Revision || cur.State != GrantActive {
			return stale(ReasonStaleRevision)
		}
		if !now.Before(cur.ExpiresAt) {
			return stale(ReasonGrantExpired)
		}
	}
	return checkedDel, nil, nil
}

// identityResolver — подмножество identity.Service, нужное адаптеру состояния.
type identityResolver interface {
	Resolve(ctx context.Context, ref identity.PrincipalRef) (identity.Identity, error)
}

type resolverStateReader struct{ r identityResolver }

// NewIdentityStateReader строит IdentityStateReader поверх identity.Service.Resolve, который возвращает
// identity только если она active И её критичный аудит подтверждён (succeeded). Соответствие ошибок
// консервативное: active — только успешный Resolve; revoked → IdentityRevoked; ErrForbidden (приостановлена
// либо аудит не подтверждён) → IdentitySuspended, то есть «непригодна»; ErrNotFound → ErrNotFound; всё
// остальное — ErrUnavailable.
func NewIdentityStateReader(r identityResolver) IdentityStateReader { return resolverStateReader{r: r} }

func (a resolverStateReader) GetPrincipalState(ctx context.Context, p identity.PrincipalRef) (identity.IdentityState, error) {
	_, err := a.r.Resolve(ctx, p)
	switch {
	case err == nil:
		return identity.IdentityActive, nil
	case errors.Is(err, identity.ErrRevoked):
		return identity.IdentityRevoked, nil
	case errors.Is(err, identity.ErrForbidden):
		return identity.IdentitySuspended, nil
	case errors.Is(err, identity.ErrNotFound):
		return "", ErrNotFound
	default:
		return "", unavailable("identity resolver")
	}
}
