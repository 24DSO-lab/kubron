// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// ResolveRequest — явный вход resolution (ADR-0055 п.1): ID группы + ExpectedRevision + доверенный requester +
// ограниченные options. Revision закрепляется запросом; 0 не wildcard.
type ResolveRequest struct {
	Actor            Actor
	GroupID          string
	ExpectedRevision uint64
	Options          ResolveOptions
}

// PreviewResolution — read-only preview (ADR-0055 п.8): разрешает группу и возвращает диагностический report
// (возможно partial). Ничего не публикует и не создаёт Plan/Approval/Run; Kubernetes только читается.
func (s *Service) PreviewResolution(ctx context.Context, req ResolveRequest) (ResolutionReport, error) {
	_, rep, err := s.run(ctx, req, false)
	return rep, err
}

// CreateSnapshot разрешает группу и публикует immutable TargetSnapshot. Snapshot создаётся ТОЛЬКО при полном
// успешном resolution; иначе возвращается ErrIncomplete вместе с диагностическим report'ом (zero Snapshot).
// Публикация атомарна относительно revision группы (Backend.PublishSnapshot). Повторов нет: revision закреплена
// запросом и монотонна, поэтому её смена необратима — возвращается ErrGroupRevisionChanged; число обращений к
// ClusterReader ограничено MaxQueryBudget, а общее время — Timeout.
func (s *Service) CreateSnapshot(ctx context.Context, req ResolveRequest) (TargetSnapshot, ResolutionReport, error) {
	return s.run(ctx, req, true)
}

type nameKey struct{ group, version, kind, ns, name string }

type clusterState struct {
	id      string
	owner   OwnerScope
	ok      bool       // ownership подтверждён и совместим: кластер можно читать
	failed  ReasonCode // причина, остановившая обработку кластера целиком
	issues  map[ReasonCode]int
	targets map[TargetIdentity]string // identity → наблюдаемая ResourceVersion
	byName  map[nameKey]string
	byUID   map[string]nameKey
	ambUID  map[string]struct{}
	ambName map[nameKey]struct{}
}

func newClusterState(id string) *clusterState {
	return &clusterState{
		id: id, issues: map[ReasonCode]int{}, targets: map[TargetIdentity]string{},
		byName: map[nameKey]string{}, byUID: map[string]nameKey{},
		ambUID: map[string]struct{}{}, ambName: map[nameKey]struct{}{},
	}
}

func (c *clusterState) issue(r ReasonCode) { c.issues[r]++ }

func (c *clusterState) fail(r ReasonCode) {
	if c.failed == "" {
		c.failed = r
	}
	c.issue(r)
}

// add добавляет наблюдаемую цель. Точный дубликат identity схлопывается; конфликт имени с другим UID либо UID с
// другим именем помечается неоднозначным (обе цели будут исключены): namespace/name никогда не подменяют UID.
func (c *clusterState) add(t TargetIdentity, rv string) (added bool) {
	if _, dup := c.targets[t]; dup {
		return false
	}
	nk := nameKey{t.Group, t.Version, t.Kind, t.Namespace, t.Name}
	if u, ok := c.byName[nk]; ok && u != t.UID {
		c.ambName[nk] = struct{}{}
	}
	if k, ok := c.byUID[t.UID]; ok && k != nk {
		c.ambUID[t.UID] = struct{}{}
	}
	if _, ok := c.byName[nk]; !ok {
		c.byName[nk] = t.UID
	}
	if _, ok := c.byUID[t.UID]; !ok {
		c.byUID[t.UID] = nk
	}
	c.targets[t] = rv
	return true
}

func (c *clusterState) dropAmbiguous() {
	for t := range c.targets {
		nk := nameKey{t.Group, t.Version, t.Kind, t.Namespace, t.Name}
		_, au := c.ambUID[t.UID]
		_, an := c.ambName[nk]
		if au || an {
			delete(c.targets, t)
			c.issue(ReasonTargetIdentityIncompl)
		}
	}
}

type runState struct {
	requester  Actor
	maxTargets int
	budget     int
	calls      int
	total      int
}

// ctxFailure сообщает, что работа должна прекратиться: отмена вызывающего либо серверный timeout.
func ctxFailure(parent, ctx context.Context) error {
	if parent.Err() != nil {
		return unavailableReason(ReasonCanceled)
	}
	if ctx.Err() != nil {
		return unavailableReason(string(ReasonResolutionTimeout))
	}
	return nil
}

func (s *Service) run(parent context.Context, req ResolveRequest, publish bool) (TargetSnapshot, ResolutionReport, error) {
	if err := req.Actor.Validate(); err != nil {
		return TargetSnapshot{}, ResolutionReport{}, err
	}
	if !validID(req.GroupID) {
		return TargetSnapshot{}, ResolutionReport{}, invalid("Group.ID")
	}
	if req.ExpectedRevision == 0 {
		return TargetSnapshot{}, ResolutionReport{}, invalid("Request.ExpectedRevision")
	}
	maxTargets, timeout, err := s.limits.effective(req.Options)
	if err != nil {
		return TargetSnapshot{}, ResolutionReport{}, err
	}
	viewer, err := s.viewer(parent, req.Actor)
	if err != nil {
		return TargetSnapshot{}, ResolutionReport{}, err
	}
	g, err := s.backend.GetGroup(parent, viewer, req.GroupID)
	if err != nil {
		return TargetSnapshot{}, ResolutionReport{}, normalize(err)
	}
	if g.Revision != req.ExpectedRevision {
		return TargetSnapshot{}, ResolutionReport{}, ErrStaleRevision
	}
	if g.State != GroupActive {
		return TargetSnapshot{}, ResolutionReport{}, ErrDeactivated
	}
	if err := g.Selector.Validate(s.limits); err != nil {
		return TargetSnapshot{}, ResolutionReport{}, err // сохранённый selector не должен превышать пределы
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	st := &runState{requester: req.Actor, maxTargets: maxTargets, budget: s.limits.MaxQueryBudget}
	var states []*clusterState
	for _, id := range g.Selector.ClusterIDs() { // отсортированы: порядок обработки детерминирован
		if err := ctxFailure(parent, ctx); err != nil {
			return TargetSnapshot{}, ResolutionReport{}, err
		}
		cs := newClusterState(id)
		if err := s.observeOwner(ctx, parent, g.Owner, cs); err != nil {
			return TargetSnapshot{}, ResolutionReport{}, err
		}
		states = append(states, cs)
	}
	for _, cs := range states {
		if !cs.ok {
			continue
		}
		if err := s.collect(ctx, parent, g, cs, st); err != nil {
			return TargetSnapshot{}, ResolutionReport{}, err
		}
		cs.dropAmbiguous()
	}

	// Ownership мог измениться во время resolution: перечитываем доверенный каталог (ADR-0055).
	for _, cs := range states {
		if !cs.ok {
			continue
		}
		co, err := s.owners.ResolveClusterOwner(ctx, cs.id)
		if err != nil {
			if cerr := ctxFailure(parent, ctx); cerr != nil {
				return TargetSnapshot{}, ResolutionReport{}, cerr
			}
			return TargetSnapshot{}, ResolutionReport{}, unavailableReason(string(ReasonOwnershipUnresolved))
		}
		if now, serr := co.Scope(); serr != nil || now != cs.owner {
			return TargetSnapshot{}, ResolutionReport{}, conflictReason(string(ReasonOwnershipUnresolved))
		}
	}

	// Revision группы перепроверяется перед сохранением; окончательная гарантия — атомарная публикация.
	g2, err := s.backend.GetGroup(ctx, viewer, req.GroupID)
	if err != nil {
		if cerr := ctxFailure(parent, ctx); cerr != nil {
			return TargetSnapshot{}, ResolutionReport{}, cerr
		}
		return TargetSnapshot{}, ResolutionReport{}, normalize(err)
	}
	if g2.Revision != g.Revision || g2.State != GroupActive {
		return TargetSnapshot{}, ResolutionReport{}, ErrGroupRevisionChanged
	}

	rep := s.buildReport(g, states)
	if !publish {
		return TargetSnapshot{}, rep, nil
	}
	if !rep.IsComplete() {
		return TargetSnapshot{}, rep, &Error{Kind: KindIncomplete}
	}
	id, err := s.newID("ts")
	if err != nil {
		return TargetSnapshot{}, rep, err
	}
	snap, err := NewSnapshot(rep, id, req.Actor)
	if err != nil {
		return TargetSnapshot{}, rep, err
	}
	pub, err := s.backend.PublishSnapshot(ctx, snap)
	if err != nil {
		if cerr := ctxFailure(parent, ctx); cerr != nil {
			return TargetSnapshot{}, rep, cerr
		}
		return TargetSnapshot{}, rep, normalize(err)
	}
	return pub, rep, nil
}

// observeOwner получает доверенный owner кластера и проверяет совместимость с owner'ом группы. Неизвестный,
// чужой и несовместимый кластер дают ОДИН И ТОТ ЖЕ код cluster_not_visible: наличие и владелец чужого кластера
// не раскрываются.
func (s *Service) observeOwner(ctx, parent context.Context, groupOwner OwnerScope, cs *clusterState) error {
	co, err := s.owners.ResolveClusterOwner(ctx, cs.id)
	if err != nil {
		if cerr := ctxFailure(parent, ctx); cerr != nil {
			return cerr
		}
		if errors.Is(err, ErrNotFound) {
			cs.fail(ReasonClusterNotVisible)
		} else {
			cs.fail(ReasonOwnershipUnresolved)
		}
		return nil
	}
	scope, err := co.Scope()
	if err != nil {
		cs.fail(ReasonOwnershipUnresolved) // неполный scope — отказ, не wildcard
		return nil
	}
	if scope != groupOwner {
		cs.fail(ReasonClusterNotVisible)
		return nil
	}
	cs.owner, cs.ok = scope, true
	return nil
}

func (s *Service) collect(ctx, parent context.Context, g Group, cs *clusterState, st *runState) error {
	for _, m := range g.Selector.Members {
		if m.ClusterID != cs.id || cs.failed != "" {
			continue
		}
		if err := s.collectMember(ctx, parent, m, cs, st); err != nil {
			return err
		}
	}
	for _, q := range g.Selector.Queries {
		if cs.failed != "" || !slices.Contains(q.ClusterIDs, cs.id) {
			continue
		}
		if err := s.collectQuery(ctx, parent, q, cs, st); err != nil {
			return err
		}
	}
	return nil
}

// readFailure классифицирует ошибку reader'а. Возвращает fatal (timeout/cancel) либо nil после пометки кластера.
func readFailure(ctx, parent context.Context, err error, cs *clusterState) error {
	if cerr := ctxFailure(parent, ctx); cerr != nil {
		return cerr
	}
	if errors.Is(err, ErrForbidden) {
		cs.fail(ReasonClusterNotVisible)
	} else {
		cs.fail(ReasonClusterUnavailable) // любая иная ошибка — fail closed
	}
	return nil
}

func (s *Service) collectMember(ctx, parent context.Context, m ObjectRef, cs *clusterState, st *runState) error {
	if st.calls >= st.budget {
		cs.fail(ReasonSelectorLimitExceeded)
		return nil
	}
	st.calls++
	obj, err := s.reader.GetObject(ctx, GetObjectRequest{Requester: st.requester, Ref: m})
	if err != nil {
		if errors.Is(err, ErrNotFound) && ctxFailure(parent, ctx) == nil {
			cs.issue(ReasonTargetNotFound)
			return nil
		}
		return readFailure(ctx, parent, err, cs)
	}
	if obj.Group != m.Group || obj.Version != m.Version || obj.Kind != m.Kind ||
		obj.Namespace != m.Namespace || obj.Name != m.Name {
		cs.issue(ReasonTargetIdentityIncompl) // reader вернул не тот объект
		return nil
	}
	s.admit(cs, st, cs.id, obj)
	return nil
}

func (s *Service) collectQuery(ctx, parent context.Context, q QuerySelector, cs *clusterState, st *runState) error {
	capacity := st.maxTargets - st.total
	if capacity <= 0 {
		cs.fail(ReasonSelectorLimitExceeded)
		return nil
	}
	if st.calls >= st.budget {
		cs.fail(ReasonSelectorLimitExceeded)
		return nil
	}
	st.calls++
	res, err := s.reader.ListObjects(ctx, ListObjectsRequest{
		Requester: st.requester, ClusterID: cs.id, Group: q.Group, Version: q.Version, Kind: q.Kind,
		Namespaces: slices.Clone(q.Namespaces), Labels: q.Labels.canonicalCopy(), Fields: slices.Clone(q.Fields),
		Limit: capacity + 1, // +1 позволяет обнаружить превышение, не читая неограниченный набор
	})
	if err != nil {
		return readFailure(ctx, parent, err, cs)
	}
	overflow := res.Truncated
	for i, o := range res.Objects {
		if i >= capacity+1 {
			overflow = true // reader нарушил Limit: лишнее игнорируется
			break
		}
		if o.Group != q.Group || o.Version != q.Version || o.Kind != q.Kind ||
			(len(q.Namespaces) > 0 && !slices.Contains(q.Namespaces, o.Namespace)) {
			cs.issue(ReasonTargetIdentityIncompl) // объект вне запроса
			continue
		}
		if !s.admit(cs, st, cs.id, o) && st.total >= st.maxTargets {
			overflow = true
		}
	}
	if overflow {
		cs.fail(ReasonSelectorLimitExceeded)
	}
	return nil
}

func (l LabelSelector) canonicalCopy() LabelSelector {
	return QuerySelector{Labels: l}.canonical().Labels
}

// admit проверяет полноту identity и добавляет цель. Возвращает true, если цель принята либо является точным
// дубликатом. Объект без UID (либо с невалидной identity) не включается: UID не подменяется namespace/name.
func (s *Service) admit(cs *clusterState, st *runState, clusterID string, o ObservedObject) bool {
	t := TargetIdentity{
		ClusterID: clusterID, Group: o.Group, Version: o.Version, Kind: o.Kind,
		Namespace: o.Namespace, Name: o.Name, UID: o.UID,
	}
	if t.validate() != nil || !validResourceVersion(o.ResourceVersion) {
		cs.issue(ReasonTargetIdentityIncompl)
		return true
	}
	if _, dup := cs.targets[t]; !dup && st.total >= st.maxTargets {
		return false
	}
	if cs.add(t, o.ResourceVersion) {
		st.total++
	}
	return true
}

func (s *Service) buildReport(g Group, states []*clusterState) ResolutionReport {
	rep := ResolutionReport{
		GroupID: g.ID, GroupRevision: g.Revision, Owner: g.Owner,
		ResolvedAt: canonTime(s.clock.Now()), Outcome: OutcomeComplete,
	}
	for _, cs := range states {
		cov := ClusterCoverage{ClusterID: cs.id, Outcome: OutcomeComplete, Targets: len(cs.targets)}
		if cs.failed != "" || len(cs.issues) > 0 {
			cov.Outcome, rep.Outcome = OutcomePartial, OutcomePartial
			cov.Reason = cs.failed
			if cov.Reason == "" {
				var rs []string
				for r := range cs.issues {
					rs = append(rs, string(r))
				}
				slices.Sort(rs)
				cov.Reason = ReasonCode(rs[0]) // детерминированно: наименьший код
			}
		}
		rep.Coverage = append(rep.Coverage, cov)
		for r, n := range cs.issues {
			rep.Issues = append(rep.Issues, Issue{ClusterID: cs.id, Reason: r, Count: n})
		}
		if cs.ok {
			rep.Owners = append(rep.Owners, OwnerObservation{ClusterID: cs.id, Owner: cs.owner})
		}
		for t, rv := range cs.targets {
			rep.Targets = append(rep.Targets, ResolvedTarget{Identity: t, ResourceVersion: rv})
		}
	}
	slices.SortFunc(rep.Issues, func(a, b Issue) int {
		if c := strings.Compare(a.ClusterID, b.ClusterID); c != 0 {
			return c
		}
		return strings.Compare(string(a.Reason), string(b.Reason))
	})
	slices.SortFunc(rep.Targets, func(a, b ResolvedTarget) int { return compareIdentity(a.Identity, b.Identity) })
	return rep
}
