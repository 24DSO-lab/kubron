// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package targettest

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/target"
)

// FakeAccess — управляемая реализация target.Access.
type FakeAccess struct {
	mu      sync.Mutex
	Admin   map[target.OwnerScope]bool // actor.ID не различается: тестам хватает owner-уровня
	Scopes  []target.OwnerScope
	Down    bool
	Authzed []target.AdminAction
}

// AuthorizeGroupAdmin реализует target.Access.
func (a *FakeAccess) AuthorizeGroupAdmin(_ context.Context, _ target.Actor, owner target.OwnerScope, action target.AdminAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Authzed = append(a.Authzed, action)
	if a.Down {
		return &target.Error{Kind: target.KindUnavailable}
	}
	if !a.Admin[owner] {
		return target.ErrForbidden
	}
	return nil
}

// ViewerFor реализует target.Access.
func (a *FakeAccess) ViewerFor(_ context.Context, _ target.Actor) (target.Viewer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Down {
		return target.Viewer{}, &target.Error{Kind: target.KindUnavailable}
	}
	return target.Viewer{Scopes: slices.Clone(a.Scopes)}, nil
}

// FakeOwners — управляемый target.OwnershipResolver.
type FakeOwners struct {
	mu     sync.Mutex
	Owners map[string]target.ClusterOwner
	Err    map[string]error
	Calls  map[string]int
	// OnResolve вызывается перед ответом (номер вызова для кластера начиная с 1) — для имитации смены ownership.
	OnResolve func(clusterID string, call int)
}

// ResolveClusterOwner реализует target.OwnershipResolver.
func (o *FakeOwners) ResolveClusterOwner(_ context.Context, id string) (target.ClusterOwner, error) {
	o.mu.Lock()
	if o.Calls == nil {
		o.Calls = map[string]int{}
	}
	o.Calls[id]++
	n := o.Calls[id]
	hook := o.OnResolve
	o.mu.Unlock()
	if hook != nil {
		hook(id, n)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.Err[id]; err != nil {
		return target.ClusterOwner{}, err
	}
	co, ok := o.Owners[id]
	if !ok {
		return target.ClusterOwner{}, target.ErrNotFound
	}
	return co, nil
}

// FakeObject — объект фейкового кластера с лейблами (для ListObjects).
type FakeObject struct {
	target.ObservedObject
	Labels map[string]string
}

// FakeReader — управляемый target.ClusterReader. Не создаёт никаких Kubernetes-клиентов.
type FakeReader struct {
	mu      sync.Mutex
	Objects map[string][]FakeObject // по ClusterID
	Err     map[string]error        // отказ кластера целиком
	Calls   int
	OnCall  func(call int)
	// Requesters — зафиксированные requester'ы (проверка, что доверенный контекст передаётся).
	Requesters []target.Actor
	Delay      time.Duration
}

// GetObject реализует target.ClusterReader.
func (r *FakeReader) GetObject(ctx context.Context, req target.GetObjectRequest) (target.ObservedObject, error) {
	n, err := r.enter(ctx, req.Requester, req.Ref.ClusterID)
	_ = n
	if err != nil {
		return target.ObservedObject{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.Objects[req.Ref.ClusterID] {
		if o.Group == req.Ref.Group && o.Version == req.Ref.Version && o.Kind == req.Ref.Kind &&
			o.Namespace == req.Ref.Namespace && o.Name == req.Ref.Name {
			return o.ObservedObject, nil
		}
	}
	return target.ObservedObject{}, target.ErrNotFound
}

// ListObjects реализует target.ClusterReader.
func (r *FakeReader) ListObjects(ctx context.Context, req target.ListObjectsRequest) (target.ListObjectsResult, error) {
	if _, err := r.enter(ctx, req.Requester, req.ClusterID); err != nil {
		return target.ListObjectsResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var res target.ListObjectsResult
	for _, o := range r.Objects[req.ClusterID] {
		if o.Group != req.Group || o.Version != req.Version || o.Kind != req.Kind {
			continue
		}
		if len(req.Namespaces) > 0 && !slices.Contains(req.Namespaces, o.Namespace) {
			continue
		}
		if !matchLabels(req.Labels, o.Labels) {
			continue
		}
		if len(res.Objects) == req.Limit {
			res.Truncated = true
			break
		}
		res.Objects = append(res.Objects, o.ObservedObject)
	}
	return res, nil
}

func (r *FakeReader) enter(ctx context.Context, who target.Actor, cluster string) (int, error) {
	r.mu.Lock()
	r.Calls++
	n := r.Calls
	r.Requesters = append(r.Requesters, who)
	hook, delay, cerr := r.OnCall, r.Delay, r.Err[cluster]
	r.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return n, ctx.Err()
		}
	}
	return n, cerr
}

func matchLabels(sel target.LabelSelector, labels map[string]string) bool {
	for k, v := range sel.MatchLabels {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	for _, e := range sel.MatchExpressions {
		got, has := labels[e.Key]
		switch e.Operator {
		case target.OpIn:
			if !has || !slices.Contains(e.Values, got) {
				return false
			}
		case target.OpNotIn:
			if has && slices.Contains(e.Values, got) {
				return false
			}
		case target.OpExists:
			if !has {
				return false
			}
		case target.OpDoesNotExist:
			if has {
				return false
			}
		}
	}
	return true
}

// SeqIDs — детерминированный target.IDGenerator для тестов.
type SeqIDs struct {
	mu sync.Mutex
	n  int
}

// NewID реализует target.IDGenerator.
func (s *SeqIDs) NewID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return prefix + "-" + itoa(s.n), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// ValidContent возвращает минимально валидное содержимое группы (один явный член).
func ValidContent(name string) target.GroupContent {
	return target.GroupContent{
		Name: name,
		Selector: target.SelectorDefinition{Members: []target.ObjectRef{
			{ClusterID: "c1", Version: "v1", Kind: "ConfigMap", Namespace: "default", Name: "cm"},
		}},
	}
}

// ValidGroup возвращает валидную новую группу (Revision 0) для прямого вызова Backend.CreateGroup.
func ValidGroup(id string, owner target.OwnerScope) target.Group {
	return target.Group{
		ID: id, Owner: owner, CreatedBy: target.Actor{Kind: target.ActorUser, ID: "u1"},
		CreatedAt: time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC), GroupContent: ValidContent("group " + id),
		State: target.GroupActive,
	}
}
