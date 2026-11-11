// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import "testing"

func TestAdminHasEverything(t *testing.T) {
	for _, p := range allPermissions {
		if !RoleAdmin.Has(p) {
			t.Errorf("admin must have %q", p)
		}
	}
	// в том числе права, которых пока нет
	if !RoleAdmin.Has(Permission("future.permission")) {
		t.Error("admin must pass any check, including for future permissions")
	}
}

func TestUserIsReadOnly(t *testing.T) {
	// secrets.read у пользователя нет: значения секретов, содержимое релизов Helm и сертификаты ему недоступны.
	allowed := map[Permission]bool{PermRead: true}
	for _, p := range allPermissions {
		if got := RoleUser.Has(p); got != allowed[p] {
			t.Errorf("user.Has(%q) = %v, expected %v", p, got, allowed[p])
		}
	}
}

func TestOperatorCanOperateButNotDestroy(t *testing.T) {
	allowed := map[Permission]bool{PermRead: true, PermSecretsRead: true, PermOperate: true, PermDiagnosticsLogs: true}
	for _, p := range allPermissions {
		if got := RoleOperator.Has(p); got != allowed[p] {
			t.Errorf("operator.Has(%q) = %v, expected %v", p, got, allowed[p])
		}
	}
	// оператор не слабее пользователя: всё, что видит user, видит и operator
	for _, p := range RoleUser.Permissions() {
		if !RoleOperator.Has(p) {
			t.Errorf("the operator lacks permission %q that the user has", p)
		}
	}
}

func TestUnknownRoleHasNothing(t *testing.T) {
	r := Role("hacker")
	if r.Valid() {
		t.Fatal("an unknown role must not be valid")
	}
	for _, p := range allPermissions {
		if r.Has(p) {
			t.Errorf("an unknown role got permission %q", p)
		}
	}
	if len(r.Permissions()) != 0 {
		t.Error("an unknown role must have no permissions")
	}
}

func TestRolesListMatchesTable(t *testing.T) {
	list := Roles()
	if len(list) != len(roles) {
		t.Fatalf("Roles() returned %d roles, the table has %d: was a role forgotten in roleOrder?", len(list), len(roles))
	}
	for _, ri := range list {
		if !ri.ID.Valid() || ri.Label == "" {
			t.Errorf("invalid role description: %+v", ri)
		}
	}
}

func TestRolePermissionsAreKnown(t *testing.T) {
	for id, d := range roles {
		for _, p := range d.permissions {
			if !KnownPermission(p) {
				t.Errorf("role %s contains unknown permission %q", id, p)
			}
		}
	}
}
