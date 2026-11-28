// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target_test

import (
	"context"
	"testing"

	"github.com/24DSO-lab/kubron/internal/target"
)

func TestOwnerScopeValidation(t *testing.T) {
	valid := []target.OwnerScope{orgA, teamA1, target.OrganizationOwner(target.LocalOrgID)}
	for _, o := range valid {
		ok(t, o.Validate())
	}
	invalid := map[string]target.OwnerScope{
		"zero value / implicit owner": {},
		"empty org id":                {Kind: target.OwnerOrganization},
		"org with team id":            {Kind: target.OwnerOrganization, OrgID: "o", TeamID: "t"},
		"team without team id":        {Kind: target.OwnerTeam, OrgID: "o"},
		"team without org id":         {Kind: target.OwnerTeam, TeamID: "t"},
		"unknown kind":                {Kind: "cluster", OrgID: "o"},
		"bad characters":              {Kind: target.OwnerOrganization, OrgID: "a/b"},
		"team in local scope":         {Kind: target.OwnerTeam, OrgID: target.LocalOrgID, TeamID: "t"},
	}
	for name, o := range invalid {
		is(t, o.Validate(), target.ErrInvalid)
		_ = name
	}
}

func TestSpoofedOwnerIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	in := func(o target.OwnerScope) target.CreateGroupInput {
		return target.CreateGroupInput{Owner: o, Content: target.GroupContent{Name: "g", Selector: target.SelectorDefinition{Members: []target.ObjectRef{member("c1", "cm")}}}}
	}
	ctx := context.Background()

	// Actor не администратор чужой Organization и чужой Team: owner из запроса — не доказательство.
	for _, spoofed := range []target.OwnerScope{orgB, teamA2, target.TeamOwner("org-b", "team-1")} {
		_, err := e.svc.CreateGroup(ctx, user, in(spoofed))
		is(t, err, target.ErrForbidden)
	}
	if e.backend.SnapshotCount() != 0 {
		t.Fatal("unexpected snapshot")
	}
	// Ничего не создано: у чужих Viewer'ов и у владельца список пуст.
	page, err := e.svc.ListGroups(ctx, user, target.ListOptions{})
	ok(t, err)
	if len(page.Groups) != 0 {
		t.Fatalf("spoofed create left a group behind: %+v", page.Groups)
	}
	// Некорректный owner отклоняется до обращения к Access.
	_, err = e.svc.CreateGroup(ctx, user, in(target.OwnerScope{}))
	is(t, err, target.ErrInvalid)
	// Легитимное создание работает.
	g, err := e.svc.CreateGroup(ctx, user, in(teamA1))
	ok(t, err)
	if g.Owner != teamA1 || g.CreatedBy != user || g.Revision != 1 {
		t.Fatalf("unexpected group: %+v", g)
	}
	// Мутации — только от человека-actor'а.
	_, err = e.svc.CreateGroup(ctx, target.Actor{Kind: target.ActorScheduler, ID: "s"}, in(orgA))
	is(t, err, target.ErrForbidden)
	// Недоступность Access — fail closed (не Forbidden, не успех).
	e.access.Down = true
	_, err = e.svc.CreateGroup(ctx, user, in(orgA))
	is(t, err, target.ErrUnavailable)
}

func TestOwnerCompatibility(t *testing.T) {
	cases := []struct {
		name    string
		group   target.OwnerScope
		cluster target.ClusterOwner
		want    bool
	}{
		{"org group, org cluster", orgA, target.ClusterOwner{OrgID: "org-a"}, true},
		{"team group, own team cluster", teamA1, target.ClusterOwner{OrgID: "org-a", TeamID: "team-1"}, true},
		{"org group is not access to team-owned cluster", orgA, target.ClusterOwner{OrgID: "org-a", TeamID: "team-1"}, false},
		{"team group, org-owned cluster is not elevated", teamA1, target.ClusterOwner{OrgID: "org-a"}, false},
		{"team group, other team", teamA1, target.ClusterOwner{OrgID: "org-a", TeamID: "team-2"}, false},
		{"foreign org", orgA, target.ClusterOwner{OrgID: "org-b"}, false},
		{"same team id in foreign org", teamA1, target.ClusterOwner{OrgID: "org-b", TeamID: "team-1"}, false},
		{"empty team id is not a wildcard", teamA1, target.ClusterOwner{OrgID: "org-a", TeamID: ""}, false},
		{"incomplete cluster owner", orgA, target.ClusterOwner{}, false},
		{"invalid group owner", target.OwnerScope{}, target.ClusterOwner{OrgID: "org-a"}, false},
	}
	for _, c := range cases {
		if got := target.OwnerCompatible(c.group, c.cluster); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
