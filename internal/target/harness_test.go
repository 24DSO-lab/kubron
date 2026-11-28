// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/target"
	"github.com/24DSO-lab/kubron/internal/target/targettest"
)

var (
	orgA   = target.OrganizationOwner("org-a")
	orgB   = target.OrganizationOwner("org-b")
	teamA1 = target.TeamOwner("org-a", "team-1")
	teamA2 = target.TeamOwner("org-a", "team-2")
	user   = target.Actor{Kind: target.ActorUser, ID: "u1"}
	t0     = time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
)

type targettestObject = targettest.FakeObject

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type env struct {
	t       *testing.T
	svc     *target.Service
	backend *targettest.MemoryBackend
	access  *targettest.FakeAccess
	owners  *targettest.FakeOwners
	reader  *targettest.FakeReader
}

func newEnv(t *testing.T, lim *target.Limits) *env {
	t.Helper()
	e := &env{
		t: t, backend: targettest.NewMemoryBackend(),
		access: &targettest.FakeAccess{
			Admin:  map[target.OwnerScope]bool{orgA: true, teamA1: true},
			Scopes: []target.OwnerScope{orgA, teamA1},
		},
		owners: &targettest.FakeOwners{Owners: map[string]target.ClusterOwner{
			"c1": {OrgID: "org-a"}, "c2": {OrgID: "org-a"}, "c3": {OrgID: "org-a"},
			"t1": {OrgID: "org-a", TeamID: "team-1"},
			"x1": {OrgID: "org-b"},
		}},
		reader: &targettest.FakeReader{Objects: map[string][]targettest.FakeObject{}},
	}
	svc, err := target.NewService(target.Deps{
		Backend: e.backend, Access: e.access, Owners: e.owners, Reader: e.reader,
		Clock: target.ClockFunc(func() time.Time { return t0 }), IDs: &targettest.SeqIDs{}, Limits: lim,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func obj(kind, ns, name, uid, rv string) targettest.FakeObject {
	return targettest.FakeObject{ObservedObject: target.ObservedObject{
		Version: "v1", Kind: kind, Namespace: ns, Name: name, UID: uid, ResourceVersion: rv,
	}}
}

func (e *env) group(owner target.OwnerScope, sel target.SelectorDefinition) target.Group {
	e.t.Helper()
	g, err := e.svc.CreateGroup(context.Background(), user, target.CreateGroupInput{
		Owner: owner, Content: target.GroupContent{Name: "g", Selector: sel},
	})
	if err != nil {
		e.t.Fatalf("create group: %v", err)
	}
	return g
}

func member(cluster, name string) target.ObjectRef {
	return target.ObjectRef{ClusterID: cluster, Version: "v1", Kind: "ConfigMap", Namespace: "default", Name: name}
}

func cmQuery(clusters ...string) target.QuerySelector {
	return target.QuerySelector{ClusterIDs: clusters, Version: "v1", Kind: "ConfigMap"}
}

func req(g target.Group) target.ResolveRequest {
	return target.ResolveRequest{Actor: user, GroupID: g.ID, ExpectedRevision: g.Revision}
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
