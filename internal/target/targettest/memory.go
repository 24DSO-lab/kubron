// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package targettest — переиспользуемые средства проверки домена target: эталонный in-memory Backend (только
// для тестов), fakes портов и контрактный набор Run, который обязана проходить любая реализация target.Backend
// (File, PostgreSQL). Не используется в production-коде.
package targettest

import (
	"context"
	"sort"
	"sync"

	"github.com/24DSO-lab/kubron/internal/target"
)

// MemoryBackend — эталонная реализация target.Backend: одна мьютексная consistency boundary, переходы строятся
// чистыми функциями домена. Хуки позволяют тестам вклиниться в момент перед записью.
type MemoryBackend struct {
	mu     sync.Mutex
	groups map[string]target.Group
	snaps  map[string]target.TargetSnapshot

	// BeforePublish вызывается в PublishSnapshot ДО входа в consistency boundary (имитация гонки с Update).
	BeforePublish func()
}

// NewMemoryBackend создаёт пустой backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{groups: map[string]target.Group{}, snaps: map[string]target.TargetSnapshot{}}
}

var _ target.Backend = (*MemoryBackend)(nil)

func ctxErr(ctx context.Context) error {
	if ctx.Err() != nil {
		return &target.Error{Kind: target.KindUnavailable}
	}
	return nil
}

// CreateGroup реализует target.Backend.
func (b *MemoryBackend) CreateGroup(ctx context.Context, g target.Group) (target.Group, error) {
	if err := ctxErr(ctx); err != nil {
		return target.Group{}, err
	}
	stored, err := g.Created()
	if err != nil {
		return target.Group{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.groups[stored.ID]; ok {
		return target.Group{}, target.ErrAlreadyExists
	}
	b.groups[stored.ID] = stored
	return stored.Clone(), nil
}

// GetGroup реализует target.Backend.
func (b *MemoryBackend) GetGroup(ctx context.Context, v target.Viewer, id string) (target.Group, error) {
	if err := ctxErr(ctx); err != nil {
		return target.Group{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.groups[id]
	if !ok || !v.CanSee(g) {
		return target.Group{}, target.ErrNotFound
	}
	return g.Clone(), nil
}

// UpdateGroup реализует target.Backend.
func (b *MemoryBackend) UpdateGroup(ctx context.Context, req target.UpdateGroupRequest) (target.Group, error) {
	if err := ctxErr(ctx); err != nil {
		return target.Group{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.groups[req.ID]
	if !ok {
		return target.Group{}, target.ErrNotFound
	}
	next, err := g.ApplyUpdate(req)
	if err != nil {
		return target.Group{}, err
	}
	b.groups[req.ID] = next
	return next.Clone(), nil
}

// DeactivateGroup реализует target.Backend.
func (b *MemoryBackend) DeactivateGroup(ctx context.Context, req target.DeactivateGroupRequest) (target.Group, error) {
	if err := ctxErr(ctx); err != nil {
		return target.Group{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.groups[req.ID]
	if !ok {
		return target.Group{}, target.ErrNotFound
	}
	next, err := g.ApplyDeactivate(req)
	if err != nil {
		return target.Group{}, err
	}
	b.groups[req.ID] = next
	return next.Clone(), nil
}

// ListGroups реализует target.Backend.
func (b *MemoryBackend) ListGroups(ctx context.Context, v target.Viewer, opts target.ListOptions) (target.GroupPage, error) {
	if err := ctxErr(ctx); err != nil {
		return target.GroupPage{}, err
	}
	if opts.Limit <= 0 {
		return target.GroupPage{}, &target.Error{Kind: target.KindInvalid, Reason: target.ReasonRequestInvalid, Field: "ListOptions.Limit"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.groups))
	for id := range b.groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var page target.GroupPage
	for _, id := range ids {
		if id <= opts.After && opts.After != "" {
			continue
		}
		g := b.groups[id]
		if !v.CanSee(g) || (g.State == target.GroupDeactivated && !opts.IncludeDeactivated) {
			continue
		}
		if len(page.Groups) == opts.Limit {
			page.NextAfter = page.Groups[len(page.Groups)-1].ID
			return page, nil
		}
		page.Groups = append(page.Groups, g.Clone())
	}
	return page, nil
}

// PublishSnapshot реализует target.Backend: проверка revision и запись выполняются под одной блокировкой.
func (b *MemoryBackend) PublishSnapshot(ctx context.Context, s target.TargetSnapshot) (target.TargetSnapshot, error) {
	if b.BeforePublish != nil {
		b.BeforePublish()
	}
	if err := ctxErr(ctx); err != nil {
		return target.TargetSnapshot{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.groups[s.GroupID]
	if !ok {
		return target.TargetSnapshot{}, target.ErrNotFound
	}
	if err := g.CheckSnapshotPublishable(s); err != nil {
		return target.TargetSnapshot{}, err
	}
	if _, dup := b.snaps[s.ID]; dup {
		return target.TargetSnapshot{}, target.ErrAlreadyExists
	}
	b.snaps[s.ID] = s.Clone()
	return s.Clone(), nil
}

// GetSnapshot реализует target.Backend.
func (b *MemoryBackend) GetSnapshot(ctx context.Context, v target.Viewer, id string) (target.TargetSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return target.TargetSnapshot{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.snaps[id]
	if !ok || !v.CanSeeOwner(s.Owner) {
		return target.TargetSnapshot{}, target.ErrNotFound
	}
	return s.Clone(), nil
}

// SnapshotCount возвращает число опубликованных snapshot'ов (для тестов).
func (b *MemoryBackend) SnapshotCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.snaps)
}
