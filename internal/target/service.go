// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Deps — зависимости Service. Все обязательны, кроме Clock/IDs/Limits (есть значения по умолчанию).
type Deps struct {
	Backend Backend
	Owners  OwnershipResolver
	Reader  ClusterReader
	Access  Access
	Clock   Clock
	IDs     IDGenerator
	Limits  *Limits // nil — DefaultLimits
}

// Service — use cases Target domain: управление группами и resolution (preview/snapshot). Не создаёт Plans,
// Approvals, Run и не выполняет мутаций Kubernetes.
type Service struct {
	backend Backend
	owners  OwnershipResolver
	reader  ClusterReader
	access  Access
	clock   Clock
	ids     IDGenerator
	limits  Limits
}

// NewService проверяет зависимости и пределы.
func NewService(d Deps) (*Service, error) {
	if d.Backend == nil || d.Owners == nil || d.Reader == nil || d.Access == nil {
		return nil, invalid("Deps")
	}
	s := &Service{backend: d.Backend, owners: d.Owners, reader: d.Reader, access: d.Access,
		clock: d.Clock, ids: d.IDs, limits: DefaultLimits()}
	if d.Limits != nil {
		if err := d.Limits.Validate(); err != nil {
			return nil, err
		}
		s.limits = *d.Limits
	}
	if s.clock == nil {
		s.clock = ClockFunc(func() time.Time { return time.Now() })
	}
	if s.ids == nil {
		s.ids = RandomIDs{}
	}
	return s, nil
}

// RandomIDs — IDGenerator на crypto/rand: prefix-<32 hex>.
type RandomIDs struct{}

// NewID реализует IDGenerator.
func (RandomIDs) NewID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", unavailable()
	}
	return prefix + "-" + hex.EncodeToString(b[:]), nil
}

// CreateGroupInput — вход создания. Owner — ЗАПРОШЕННЫЙ владелец: он не доказательство, а предмет проверки
// Access.AuthorizeGroupAdmin (клиент не может назначить произвольный Organization/Team).
type CreateGroupInput struct {
	Owner   OwnerScope
	Content GroupContent
}

func (s *Service) newID(prefix string) (string, error) {
	id, err := s.ids.NewID(prefix)
	if err != nil || !validID(id) {
		return "", unavailable()
	}
	return id, nil
}

func mutationActor(a Actor) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.Kind != ActorUser {
		return forbidden() // конфигурацию Kubron меняет только человек
	}
	return nil
}

func (s *Service) authorize(ctx context.Context, a Actor, owner OwnerScope, act AdminAction) error {
	err := s.access.AuthorizeGroupAdmin(ctx, a, owner, act)
	if err == nil {
		return nil
	}
	if e, ok := asError(err); ok && e.Kind == KindForbidden {
		return forbidden()
	}
	return unavailable()
}

func (s *Service) viewer(ctx context.Context, a Actor) (Viewer, error) {
	if err := a.Validate(); err != nil {
		return Viewer{}, err
	}
	v, err := s.access.ViewerFor(ctx, a)
	if err != nil {
		return Viewer{}, unavailable()
	}
	if v.Validate() != nil {
		return Viewer{}, unavailable()
	}
	return v, nil
}

// CreateGroup создаёт группу. Порядок: форма → авторизация в owner scope → backend.CreateGroup.
func (s *Service) CreateGroup(ctx context.Context, actor Actor, in CreateGroupInput) (Group, error) {
	if err := mutationActor(actor); err != nil {
		return Group{}, err
	}
	if err := in.Owner.Validate(); err != nil {
		return Group{}, err
	}
	if err := in.Content.Validate(in.Owner, s.limits); err != nil {
		return Group{}, err
	}
	if err := s.authorize(ctx, actor, in.Owner, AdminGroupCreate); err != nil {
		return Group{}, err
	}
	id, err := s.newID("tg")
	if err != nil {
		return Group{}, err
	}
	g, err := s.backend.CreateGroup(ctx, Group{
		ID: id, Owner: in.Owner, CreatedBy: actor, CreatedAt: s.clock.Now(),
		GroupContent: in.Content, State: GroupActive,
	}.Canonical())
	if err != nil {
		return Group{}, normalize(err)
	}
	return g, nil
}

// GetGroup возвращает видимую actor'у группу; невидимая неотличима от отсутствующей.
func (s *Service) GetGroup(ctx context.Context, actor Actor, id string) (Group, error) {
	v, err := s.viewer(ctx, actor)
	if err != nil {
		return Group{}, err
	}
	if !validID(id) {
		return Group{}, invalid("Group.ID")
	}
	g, err := s.backend.GetGroup(ctx, v, id)
	if err != nil {
		return Group{}, normalize(err)
	}
	return g, nil
}

// ListGroups возвращает страницу видимых групп; размер страницы ограничен серверным пределом.
func (s *Service) ListGroups(ctx context.Context, actor Actor, opts ListOptions) (GroupPage, error) {
	v, err := s.viewer(ctx, actor)
	if err != nil {
		return GroupPage{}, err
	}
	if opts.Limit < 0 || (opts.After != "" && !validID(opts.After)) {
		return GroupPage{}, invalid("ListOptions")
	}
	if opts.Limit == 0 || opts.Limit > s.limits.MaxListPage {
		opts.Limit = s.limits.MaxListPage
	}
	p, err := s.backend.ListGroups(ctx, v, opts)
	if err != nil {
		return GroupPage{}, normalize(err)
	}
	return p, nil
}

// adminTarget находит группу, видимую actor'у, и проверяет право администрировать её OWNER scope. Sharing права
// изменять не даёт: получатель видит группу, но получает ErrForbidden.
func (s *Service) adminTarget(ctx context.Context, actor Actor, id string, act AdminAction, expected uint64) (Group, error) {
	if err := mutationActor(actor); err != nil {
		return Group{}, err
	}
	if !validID(id) {
		return Group{}, invalid("Group.ID")
	}
	if expected == 0 {
		return Group{}, invalid("Request.ExpectedRevision") // 0 не wildcard
	}
	v, err := s.viewer(ctx, actor)
	if err != nil {
		return Group{}, err
	}
	g, err := s.backend.GetGroup(ctx, v, id)
	if err != nil {
		return Group{}, normalize(err)
	}
	if err := s.authorize(ctx, actor, g.Owner, act); err != nil {
		return Group{}, err
	}
	return g, nil
}

// UpdateGroup меняет содержимое по CAS: stale writer получает ErrStaleRevision; успех увеличивает revision
// ровно на 1. Owner неизменяем (в запросе его нет).
func (s *Service) UpdateGroup(ctx context.Context, actor Actor, id string, expected uint64, c GroupContent) (Group, error) {
	g, err := s.adminTarget(ctx, actor, id, AdminGroupUpdate, expected)
	if err != nil {
		return Group{}, err
	}
	if err := c.Validate(g.Owner, s.limits); err != nil {
		return Group{}, err
	}
	out, err := s.backend.UpdateGroup(ctx, UpdateGroupRequest{
		ID: id, ExpectedRevision: expected, Content: c.Canonical(), Actor: actor, At: s.clock.Now(),
	})
	if err != nil {
		return Group{}, normalize(err)
	}
	return out, nil
}

// DeactivateGroup выполняет tombstone-деактивацию по CAS (повторной активации и физического удаления нет).
func (s *Service) DeactivateGroup(ctx context.Context, actor Actor, id string, expected uint64) (Group, error) {
	if _, err := s.adminTarget(ctx, actor, id, AdminGroupDeactivate, expected); err != nil {
		return Group{}, err
	}
	out, err := s.backend.DeactivateGroup(ctx, DeactivateGroupRequest{
		ID: id, ExpectedRevision: expected, Actor: actor, At: s.clock.Now(),
	})
	if err != nil {
		return Group{}, normalize(err)
	}
	return out, nil
}

// GetSnapshot возвращает опубликованный snapshot, если его owner scope виден actor'у.
func (s *Service) GetSnapshot(ctx context.Context, actor Actor, id string) (TargetSnapshot, error) {
	v, err := s.viewer(ctx, actor)
	if err != nil {
		return TargetSnapshot{}, err
	}
	if !validID(id) {
		return TargetSnapshot{}, invalid("Snapshot.ID")
	}
	snap, err := s.backend.GetSnapshot(ctx, v, id)
	if err != nil {
		return TargetSnapshot{}, normalize(err)
	}
	return snap, nil
}
