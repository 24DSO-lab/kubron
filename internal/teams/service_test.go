// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package teams

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
)

type fakeDir struct {
	teams   []auth.Team
	err     error
	created string
	members map[string]auth.Role
}

func (d *fakeDir) DefaultTeam() string         { return "" }
func (d *fakeDir) Teams() ([]auth.Team, error) { return d.teams, d.err }
func (d *fakeDir) CreateTeam(name string) (*auth.Team, error) {
	if d.err != nil {
		return nil, d.err
	}
	d.created = name
	return &auth.Team{ID: "t-new", Name: name}, nil
}
func (d *fakeDir) RenameTeam(id, name string) error { return d.err }
func (d *fakeDir) DeleteTeam(id string) error       { return d.err }
func (d *fakeDir) Members(teamID string) ([]auth.Member, error) {
	return []auth.Member{{UserID: "u1", Username: "ann", Role: auth.RoleAdmin}}, d.err
}
func (d *fakeDir) SetMember(teamID, userID string, role auth.Role) error {
	if d.err != nil {
		return d.err
	}
	if d.members == nil {
		d.members = map[string]auth.Role{}
	}
	d.members[userID] = role
	return nil
}
func (d *fakeDir) RemoveMember(teamID, userID string) error   { return d.err }
func (d *fakeDir) MoveCluster(clusterID, teamID string) error { return d.err }

type fakeAccess struct {
	orgAdmin bool
	roles    map[string]auth.Role // team → роль пользователя
}

func (a fakeAccess) CheckTeam(p auth.Permission, teamID string) (auth.Actor, error) {
	if r, ok := a.roles[teamID]; ok {
		return auth.Actor{Username: "me", Role: r}, nil
	}
	return auth.Actor{}, auth.ErrTeamNotFound
}

func (a fakeAccess) Authorize(p auth.Permission) error {
	if p == auth.PermUsersManage && a.orgAdmin {
		return nil
	}
	return auth.ErrForbidden
}

func (a fakeAccess) Actor() auth.Actor {
	return auth.Actor{Username: "me", Role: auth.RoleAdmin, Source: "10.0.0.1"}
}

type recorder struct{ events []audit.Event }

func (r *recorder) Record(e audit.Event) { r.events = append(r.events, e) }

func TestNoDirectoryMeansNoTeams(t *testing.T) {
	s := New(nil, fakeAccess{}, nil, nil)
	if _, err := s.List(); !errors.Is(err, ErrNoTeams) {
		t.Fatalf("List: %v", err)
	}
	if _, err := s.Create("x"); !errors.Is(err, ErrNoTeams) {
		t.Fatalf("Create: %v", err)
	}
	for name, err := range map[string]error{
		"Rename":       s.Rename("t", "n"),
		"Delete":       s.Delete("t"),
		"SetMember":    s.SetMember("t", "u", "user"),
		"RemoveMember": s.RemoveMember("t", "u"),
	} {
		if !errors.Is(err, ErrNoTeams) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.Members("t"); !errors.Is(err, ErrNoTeams) {
		t.Fatalf("Members: %v", err)
	}
}

func TestListShowsOnlyMyTeamsUnlessOrgAdmin(t *testing.T) {
	dir := &fakeDir{teams: []auth.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	acc := fakeAccess{roles: map[string]auth.Role{"a": auth.RoleAdmin, "c": auth.RoleUser}}

	got, err := New(dir, acc, nil, nil).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Team.ID != "a" || got[0].MyRole != string(auth.RoleAdmin) || got[1].Team.ID != "c" {
		t.Fatalf("member view: %+v", got)
	}

	acc.orgAdmin = true
	got, err = New(dir, acc, nil, nil).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].Team.ID != "b" || got[1].MyRole != "" {
		t.Fatalf("org admin must see all teams, and the foreign one without a role: %+v", got)
	}
}

func TestListPropagatesDirectoryError(t *testing.T) {
	boom := errors.New("db down")
	if _, err := New(&fakeDir{err: boom}, fakeAccess{}, nil, nil).List(); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestCreateRecordsTheResult(t *testing.T) {
	rec := &recorder{}
	dir := &fakeDir{}
	team, err := New(dir, fakeAccess{}, rec, nil).Create("  Platform ")
	if err != nil || team.ID != "t-new" {
		t.Fatalf("%v %+v", err, team)
	}
	if len(rec.events) != 1 {
		t.Fatalf("events: %+v", rec.events)
	}
	e := rec.events[0]
	if e.Action != "CreateTeam" || e.Result != audit.ResultOK || e.TeamID != "t-new" || e.Detail != "name=Platform" ||
		e.User != "me" || e.Source != "10.0.0.1" {
		t.Fatalf("event: %+v", e)
	}
}

func TestFailureIsRecordedAsFailed(t *testing.T) {
	rec := &recorder{}
	s := New(&fakeDir{err: errors.New("exists")}, fakeAccess{}, rec, nil)
	if _, err := s.Create("x"); err == nil {
		t.Fatal("want an error")
	}
	if len(rec.events) != 1 || rec.events[0].Result != audit.ResultFailed || rec.events[0].TeamID != "" {
		t.Fatalf("events: %+v", rec.events)
	}
}

func TestNoRecorderOrAccessMeansNoJournal(t *testing.T) {
	// не должно паниковать: в десктопе журнала или сессии может не быть
	if _, err := New(&fakeDir{}, fakeAccess{}, nil, nil).Create("x"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(&fakeDir{}, nil, &recorder{}, nil).Create("x"); err != nil {
		t.Fatal(err)
	}
	if got, err := New(&fakeDir{teams: []auth.Team{{ID: "a"}}}, nil, nil, nil).List(); err != nil || len(got) != 0 {
		t.Fatalf("without a session nothing is visible: %v %+v", err, got)
	}
}

func TestSetMemberTrimsTheRoleForTheStoreOnly(t *testing.T) {
	rec := &recorder{}
	dir := &fakeDir{}
	if err := New(dir, fakeAccess{}, rec, nil).SetMember("t1", "u1", " user "); err != nil {
		t.Fatal(err)
	}
	if dir.members["u1"] != auth.Role("user") {
		t.Fatalf("stored role %q", dir.members["u1"])
	}
	if rec.events[0].Detail != "user=u1 role= user " || rec.events[0].Action != "SetTeamMember" {
		t.Fatalf("event: %+v", rec.events[0])
	}
}

func TestRemoveMemberRevokesTheirSignInsOnlyOnSuccess(t *testing.T) {
	var gotUser, gotTeam, gotReason string
	hook := func(u, tm, reason string) error { gotUser, gotTeam, gotReason = u, tm, reason; return nil }

	if err := New(&fakeDir{}, fakeAccess{}, nil, hook).RemoveMember("t1", "u1"); err != nil {
		t.Fatal(err)
	}
	if gotUser != "u1" || gotTeam != "t1" || gotReason != "removed-from-team" {
		t.Fatalf("hook: %q %q %q", gotUser, gotTeam, gotReason)
	}

	gotUser = ""
	boom := errors.New("db down")
	if err := New(&fakeDir{err: boom}, fakeAccess{}, nil, hook).RemoveMember("t1", "u1"); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if gotUser != "" {
		t.Fatal("sign-ins must not be revoked when the removal failed")
	}
}

func TestRemoveMemberReturnsTheRevokeError(t *testing.T) {
	boom := errors.New("revoke failed")
	err := New(&fakeDir{}, fakeAccess{}, nil, func(string, string, string) error { return boom }).RemoveMember("t", "u")
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}
