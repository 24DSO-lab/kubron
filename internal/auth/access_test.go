// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"testing"
)

// fakeAccess — две команды: red (кластеры c1, c2) и blue (кластер c3).
type fakeAccess struct {
	clusters map[string]string          // кластер → команда
	members  map[string]map[string]Role // пользователь → команда → роль
	orgAdmin map[string]bool
}

func (f *fakeAccess) ClusterTeam(id string) (string, bool) { t, ok := f.clusters[id]; return t, ok }
func (f *fakeAccess) TeamRole(u, t string) (Role, bool)    { r, ok := f.members[u][t]; return r, ok }
func (f *fakeAccess) TeamRoles(u string) map[string]Role   { return f.members[u] }
func (f *fakeAccess) IsOrgAdmin(u string) bool             { return f.orgAdmin[u] }

// accessFixture: admin (org-admin, без членства), bob (оператор red), eve (админ blue).
func accessFixture(t *testing.T) (admin, bob, eve *Service, fa *fakeAccess) {
	t.Helper()
	s, bobInfo := setupWithUser(t)
	loginAdmin(t, s)
	eveInfo, err := s.CreateUser("eve", "temp-pass-2", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	adminInfo, _ := s.Current()
	fa = &fakeAccess{
		clusters: map[string]string{"c1": "red", "c2": "red", "c3": "blue"},
		members: map[string]map[string]Role{
			bobInfo.ID: {"red": RoleOperator},
			eveInfo.ID: {"blue": RoleAdmin},
		},
		orgAdmin: map[string]bool{adminInfo.ID: true},
	}
	s.SetAccess(fa)
	admin = s
	bob = s.NewSession("")
	loginAs(t, bob, "bob", bobPass)
	eve = s.NewSession("")
	loginAs(t, eve, "eve", "temp-pass-2")
	if err := eve.ChangePassword(t.Context(), "temp-pass-2", "eves-own-pass"); err != nil {
		t.Fatal(err)
	}
	return admin, bob, eve, fa
}

func TestAccessRoleComesFromTeamOfCluster(t *testing.T) {
	_, bob, eve, _ := accessFixture(t)

	// bob — оператор команды red: operate своим, но не write; чужой кластер — «не найдено», не «запрещено».
	if err := bob.AuthorizeCluster(PermOperate, "c1"); err != nil {
		t.Errorf("operate в своей команде: %v", err)
	}
	if err := bob.AuthorizeCluster(PermWrite, "c1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("write оператору: %v", err)
	}
	if err := bob.AuthorizeCluster(PermRead, "c3"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("чужой кластер: %v", err)
	}
	if err := bob.AuthorizeCluster(PermRead, "nope"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("несуществующий кластер должен выглядеть так же, как чужой: %v", err)
	}
	// eve — админ blue: всё в blue, ничего в red.
	if err := eve.AuthorizeCluster(PermExec, "c3"); err != nil {
		t.Errorf("exec админу команды: %v", err)
	}
	if err := eve.AuthorizeCluster(PermRead, "c1"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("eve в red: %v", err)
	}
}

func TestOrgAdminHasNoClusterDataWithoutMembership(t *testing.T) {
	admin, _, _, _ := accessFixture(t)
	for _, p := range []Permission{PermRead, PermSecretsRead, PermWrite, PermExec} {
		if err := admin.AuthorizeCluster(p, "c1"); !errors.Is(err, ErrClusterNotFound) {
			t.Errorf("org-admin без членства, %s: %v", p, err)
		}
	}
	if admin.CanSeeCluster("c1") {
		t.Error("org-admin без членства не должен видеть кластер")
	}
	// Но управлять пользователями он может.
	if err := admin.Authorize(PermUsersManage); err != nil {
		t.Errorf("users.manage org-admin: %v", err)
	}
}

func TestOrgPermissionsNeedOrgAdmin(t *testing.T) {
	_, bob, eve, _ := accessFixture(t)
	for name, v := range map[string]*Service{"bob": bob, "eve": eve} {
		if err := v.Authorize(PermUsersManage); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s users.manage: %v", name, err)
		}
		if err := v.Authorize(PermAuditRead); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s audit.read: %v", name, err)
		}
	}
}

func TestClusterlessChecksNeedPermissionInSomeTeam(t *testing.T) {
	admin, bob, eve, _ := accessFixture(t)
	// Списки и переключение: право хотя бы в одной команде.
	if err := bob.Authorize(PermRead); err != nil {
		t.Errorf("bob read: %v", err)
	}
	if err := bob.Authorize(PermExec); !errors.Is(err, ErrForbidden) {
		t.Errorf("bob exec без кластера: %v", err)
	}
	if err := admin.Authorize(PermRead); !errors.Is(err, ErrForbidden) {
		t.Errorf("org-admin без команд не читает: %v", err)
	}
	// Добавление кластера — админ какой-либо команды.
	if err := eve.Authorize(PermClusterManage); err != nil {
		t.Errorf("eve clusters.manage: %v", err)
	}
	if err := bob.Authorize(PermClusterManage); !errors.Is(err, ErrForbidden) {
		t.Errorf("bob clusters.manage: %v", err)
	}
}

func TestAccessIsLiveAndFiltersClusters(t *testing.T) {
	_, bob, _, fa := accessFixture(t)
	if !bob.CanSeeCluster("c2") || bob.CanSeeCluster("c3") || bob.CanSeeCluster("nope") {
		t.Error("видимость кластеров")
	}
	// Исключение из команды действует сразу, без перезахода.
	for u := range fa.members {
		delete(fa.members[u], "red")
	}
	if err := bob.AuthorizeCluster(PermRead, "c1"); !errors.Is(err, ErrClusterNotFound) {
		t.Errorf("после исключения: %v", err)
	}
	// Перенос кластера в другую команду: доступ следует за владельцем.
	fa.clusters["c1"] = "blue"
	if bob.CanSeeCluster("c1") {
		t.Error("кластер перенесён в чужую команду")
	}
}

func TestActorCarriesTeamRoleAndAuthChecksStillApply(t *testing.T) {
	_, bob, _, _ := accessFixture(t)
	a, err := bob.CheckCluster(PermRead, "c1")
	if err != nil || a.Username != "bob" || a.Role != RoleOperator {
		t.Errorf("%+v %v", a, err)
	}
	bob.Logout()
	if _, err := bob.CheckCluster(PermRead, "c1"); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("без входа: %v", err)
	}
	if bob.CanSeeCluster("c1") {
		t.Error("без входа кластеры не видны")
	}
}

func TestWithoutAccessLegacyRolesApply(t *testing.T) {
	s, _ := setupWithUser(t)
	loginAdmin(t, s)
	if err := s.AuthorizeCluster(PermWrite, "anything"); err != nil {
		t.Errorf("десктоп: администратор всегда может: %v", err)
	}
	if !s.CanSeeCluster("anything") {
		t.Error("десктоп: вошедший видит все кластеры")
	}
	v := s.NewSession("")
	loginAs(t, v, "bob", bobPass)
	if err := v.AuthorizeCluster(PermWrite, "x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("десктоп: роль user не пишет: %v", err)
	}
}

func TestCheckTeam(t *testing.T) {
	admin, bob, eve, _ := accessFixture(t)
	// eve — админ blue: управляет составом и кластерами blue, но не red.
	if err := eve.AuthorizeTeam(PermUsersManage, "blue"); err != nil {
		t.Errorf("админ команды управляет составом: %v", err)
	}
	if err := eve.AuthorizeTeam(PermClusterManage, "blue"); err != nil {
		t.Errorf("админ команды управляет кластерами: %v", err)
	}
	if err := eve.AuthorizeTeam(PermUsersManage, "red"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("чужая команда: %v", err)
	}
	// bob — оператор red: состав не меняет, но о существовании команды знает.
	if err := bob.AuthorizeTeam(PermUsersManage, "red"); !errors.Is(err, ErrForbidden) {
		t.Errorf("оператор меняет состав: %v", err)
	}
	if err := bob.AuthorizeTeam(PermUsersManage, "blue"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("оператор, чужая команда: %v", err)
	}
	// org-admin: состав любой команды — да; кластеры команды без членства — нет.
	if err := admin.AuthorizeTeam(PermUsersManage, "red"); err != nil {
		t.Errorf("org-admin и состав команды: %v", err)
	}
	if err := admin.AuthorizeTeam(PermClusterManage, "red"); !errors.Is(err, ErrForbidden) {
		t.Errorf("org-admin и кластеры чужой команды: %v", err)
	}
	// Без входа.
	bob.Logout()
	if err := bob.AuthorizeTeam(PermUsersManage, "red"); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("без входа: %v", err)
	}
}

func TestCheckTeamWithoutAccessUsesGlobalRole(t *testing.T) {
	s, _ := setupWithUser(t)
	loginAdmin(t, s)
	if err := s.AuthorizeTeam(PermClusterManage, ""); err != nil {
		t.Errorf("десктоп: %v", err)
	}
	v := s.NewSession("")
	loginAs(t, v, "bob", bobPass)
	if err := v.AuthorizeTeam(PermClusterManage, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("десктоп, роль user: %v", err)
	}
}

func TestActorContextAndTeamsWith(t *testing.T) {
	_, bob, eve, _ := accessFixture(t)
	a, err := bob.CheckCluster(PermRead, "c1")
	if err != nil || a.ClusterID != "c1" || a.TeamID != "red" {
		t.Errorf("контекст для журнала: %+v %v", a, err)
	}
	// Отказ в чужой команде не раскрывает её в журнале как «свою» (команда неизвестна — кластер не найден).
	a, _ = bob.CheckCluster(PermRead, "c3")
	if a.TeamID != "" {
		t.Errorf("чужая команда в контексте: %+v", a)
	}
	a, _ = eve.CheckTeam(PermUsersManage, "blue")
	if a.TeamID != "blue" {
		t.Errorf("%+v", a)
	}
	if got := eve.TeamsWith(PermClusterManage); len(got) != 1 || got[0] != "blue" {
		t.Errorf("команды, где eve админ: %v", got)
	}
	if got := bob.TeamsWith(PermClusterManage); len(got) != 0 {
		t.Errorf("оператор не админ: %v", got)
	}
}
