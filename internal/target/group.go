// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"slices"
	"time"
)

// Sharing — отдельная ACL/visibility metadata (ADR-0054 п.7). Sharing не добавляет grants, delegation,
// Kubernetes RBAC и право изменять группу: получатель лишь ВИДИТ группу. Получатели — только scope'ы той же
// Organization, что и владелец группы.
type Sharing struct {
	SharedWith []OwnerScope
}

// GroupContent — изменяемое содержимое группы. В Phase 2 ЛЮБОЕ принятое изменение содержимого увеличивает
// revision: «косметических» исключений нет (ADR-0054).
type GroupContent struct {
	Name     string
	Selector SelectorDefinition
	Sharing  Sharing
}

// GroupState — состояние жизненного цикла.
type GroupState string

const (
	GroupActive GroupState = "active"
	// GroupDeactivated — tombstone. Повторной активации и физического удаления в Phase 2 нет.
	GroupDeactivated GroupState = "deactivated"
)

// Group — агрегат Target Group (ADR-0054 п.2).
type Group struct {
	ID        string     // непрозрачный
	Owner     OwnerScope // неизменяем после создания
	CreatedBy Actor      // только provenance
	CreatedAt time.Time
	GroupContent
	State         GroupState
	Revision      uint64 // монотонная, ≥1 у сохранённой группы
	UpdatedBy     Actor
	UpdatedAt     time.Time
	DeactivatedAt time.Time // нулевое, пока группа активна
}

func canonTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Round(0)
}

func (s Sharing) canonical() Sharing {
	if len(s.SharedWith) == 0 {
		return Sharing{}
	}
	out := slices.Clone(s.SharedWith)
	slices.SortFunc(out, compareOwners)
	return Sharing{SharedWith: slices.Compact(out)}
}

func (s Sharing) validate(owner OwnerScope, l Limits) error {
	if len(s.SharedWith) > l.MaxSharedWith {
		return invalid("Sharing.size")
	}
	seen := map[OwnerScope]struct{}{}
	for _, o := range s.SharedWith {
		if err := o.Validate(); err != nil {
			return invalid("Sharing.SharedWith")
		}
		if o.OrgID != owner.OrgID || o == owner {
			return invalid("Sharing.SharedWith") // чужая Organization либо сам владелец
		}
		if _, dup := seen[o]; dup {
			return invalid("Sharing.SharedWith")
		}
		seen[o] = struct{}{}
	}
	return nil
}

// Canonical возвращает глубокую каноническую копию содержимого.
func (c GroupContent) Canonical() GroupContent {
	return GroupContent{Name: c.Name, Selector: c.Selector.Canonical(), Sharing: c.Sharing.canonical()}
}

// Validate проверяет содержимое для владельца с серверными пределами l.
func (c GroupContent) Validate(owner OwnerScope, l Limits) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	if !validDisplayName(c.Name) {
		return invalid("Group.Name")
	}
	if err := c.Selector.Validate(l); err != nil {
		return err
	}
	return c.Sharing.validate(owner, l)
}

// Canonical возвращает глубокую каноническую копию (время UTC, списки отсортированы).
func (g Group) Canonical() Group {
	g.GroupContent = g.GroupContent.Canonical()
	g.CreatedAt, g.UpdatedAt, g.DeactivatedAt = canonTime(g.CreatedAt), canonTime(g.UpdatedAt), canonTime(g.DeactivatedAt)
	return g
}

// Clone — глубокая копия; результат не разделяет память с исходным (хранилища обязаны отдавать копии).
func (g Group) Clone() Group { return g.Canonical() }

// validate проверяет структурные инварианты. stored=false — группа на создание (Revision 0, active).
// Пределы — жёсткие потолки DefaultLimits: backend не зависит от конфигурации сервера.
func (g Group) validate(stored bool) error {
	if !validID(g.ID) {
		return invalid("Group.ID")
	}
	if err := g.Owner.Validate(); err != nil {
		return err
	}
	if err := g.CreatedBy.Validate(); err != nil {
		return err
	}
	if g.CreatedAt.IsZero() {
		return invalid("Group.CreatedAt")
	}
	if err := g.GroupContent.Validate(g.Owner, DefaultLimits()); err != nil {
		return err
	}
	switch g.State {
	case GroupActive:
		if !g.DeactivatedAt.IsZero() {
			return invalid("Group.DeactivatedAt")
		}
	case GroupDeactivated:
		if !stored || g.DeactivatedAt.IsZero() {
			return invalid("Group.State")
		}
	default:
		return invalid("Group.State")
	}
	if !stored {
		if g.Revision != 0 {
			return invalid("Group.Revision")
		}
		return nil
	}
	if g.Revision == 0 {
		return invalid("Group.Revision")
	}
	if err := g.UpdatedBy.Validate(); err != nil {
		return err
	}
	if g.UpdatedAt.IsZero() || g.UpdatedAt.Before(g.CreatedAt) {
		return invalid("Group.UpdatedAt")
	}
	return nil
}

// Validate проверяет сохранённую группу (Revision ≥ 1).
func (g Group) Validate() error { return g.validate(true) }

// Created превращает группу на создание (Revision 0, active) в первую сохранённую версию (Revision 1).
// Backend вызывает её внутри своей атомарной операции Create.
func (g Group) Created() (Group, error) {
	g.State = GroupActive
	if err := g.Canonical().validate(false); err != nil {
		return Group{}, err
	}
	g = g.Canonical()
	g.Revision = 1
	g.UpdatedBy, g.UpdatedAt = g.CreatedBy, g.CreatedAt
	return g, g.validate(true)
}

// UpdateGroupRequest — запрос на изменение содержимого по CAS. Owner в нём нет: он неизменяем.
type UpdateGroupRequest struct {
	ID               string
	ExpectedRevision uint64 // 0 не wildcard
	Content          GroupContent
	Actor            Actor
	At               time.Time
}

// DeactivateGroupRequest — запрос на tombstone-деактивацию по CAS.
type DeactivateGroupRequest struct {
	ID               string
	ExpectedRevision uint64
	Actor            Actor
	At               time.Time
}

func nextTimes(g Group, at time.Time) time.Time {
	at = canonTime(at)
	if at.Before(g.UpdatedAt) {
		return g.UpdatedAt // метки времени не идут назад
	}
	return at
}

// ApplyUpdate — единственная чистая функция перехода «обновление»: backend (File, PostgreSQL, in-memory) вызывает
// её внутри своей атомарной операции, поэтому правила идентичны. Порядок проверок: форма ввода → revision
// (stale_revision) → состояние (group_deactivated). Каждый успех увеличивает Revision ровно на 1.
func (g Group) ApplyUpdate(req UpdateGroupRequest) (Group, error) {
	if req.ID != g.ID {
		return Group{}, invalid("Request.ID")
	}
	if req.ExpectedRevision == 0 {
		return Group{}, invalid("Request.ExpectedRevision")
	}
	if err := req.Actor.Validate(); err != nil {
		return Group{}, err
	}
	if req.At.IsZero() {
		return Group{}, invalid("Request.At")
	}
	content := req.Content.Canonical()
	if err := content.Validate(g.Owner, DefaultLimits()); err != nil {
		return Group{}, err
	}
	if g.Revision != req.ExpectedRevision {
		return Group{}, ErrStaleRevision
	}
	if g.State != GroupActive {
		return Group{}, ErrDeactivated
	}
	next := g.Clone()
	next.GroupContent = content
	next.Revision = g.Revision + 1
	next.UpdatedBy, next.UpdatedAt = req.Actor, nextTimes(g, req.At)
	return next, next.validate(true)
}

// ApplyDeactivate — чистая функция перехода «деактивация» (tombstone). Revision увеличивается ровно на 1.
func (g Group) ApplyDeactivate(req DeactivateGroupRequest) (Group, error) {
	if req.ID != g.ID {
		return Group{}, invalid("Request.ID")
	}
	if req.ExpectedRevision == 0 {
		return Group{}, invalid("Request.ExpectedRevision")
	}
	if err := req.Actor.Validate(); err != nil {
		return Group{}, err
	}
	if req.At.IsZero() {
		return Group{}, invalid("Request.At")
	}
	if g.Revision != req.ExpectedRevision {
		return Group{}, ErrStaleRevision
	}
	if g.State != GroupActive {
		return Group{}, ErrDeactivated
	}
	next := g.Clone()
	next.State = GroupDeactivated
	next.Revision = g.Revision + 1
	next.UpdatedBy, next.UpdatedAt = req.Actor, nextTimes(g, req.At)
	next.DeactivatedAt = next.UpdatedAt
	return next, next.validate(true)
}

// CheckSnapshotPublishable проверяет, что snapshot можно опубликовать против ТЕКУЩЕГО состояния группы.
// Backend вызывает её в той же consistency boundary, что и запись snapshot (File — под одной блокировкой,
// PostgreSQL — в одной транзакции); повторного чтения revision до/после записи недостаточно (ADR-0055).
func (g Group) CheckSnapshotPublishable(s TargetSnapshot) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.GroupID != g.ID || s.Owner != g.Owner {
		return invalid("Snapshot.Group")
	}
	if g.Revision != s.GroupRevision || g.State != GroupActive {
		return ErrGroupRevisionChanged
	}
	return nil
}
