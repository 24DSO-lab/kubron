// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"slices"
	"testing"
)

// Права операционных модулей (ADR-0026 §5): diagnostics.logs и baseline.manage.

func TestOpsPermissionsMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		perm Permission
		role Role
		want bool
	}{
		{PermDiagnosticsLogs, RoleAdmin, true},
		{PermDiagnosticsLogs, RoleOperator, true},
		{PermDiagnosticsLogs, RoleUser, false},
		{PermBaselineManage, RoleAdmin, true},
		{PermBaselineManage, RoleOperator, false},
		{PermBaselineManage, RoleUser, false},
	}
	for _, tc := range tests {
		t.Run(string(tc.perm)+"/"+string(tc.role), func(t *testing.T) {
			t.Parallel()
			if got := tc.role.Has(tc.perm); got != tc.want {
				t.Fatalf("%s.Has(%q) = %v, want %v", tc.role, tc.perm, got, tc.want)
			}
		})
	}
}

func TestOpsPermissionsRegistered(t *testing.T) {
	t.Parallel()
	for _, p := range []Permission{PermDiagnosticsLogs, PermBaselineManage} {
		if !KnownPermission(p) {
			t.Errorf("%q is not a known permission", p)
		}
		if !slices.Contains(allPermissions, p) {
			t.Errorf("%q is missing from allPermissions", p)
		}
		// Право действует внутри кластера: определяется ролью в команде-владельце (ADR-0006, ADR-0012).
		if !ClusterScoped(p) {
			t.Errorf("%q must be cluster-scoped", p)
		}
	}
}

// Оператор получает diagnostics.logs явной записью в таблице ролей, а не «по умолчанию»: новое право не расширяет
// роли молча (ADR-0026 §5).
func TestOperatorDiagnosticsLogsIsExplicit(t *testing.T) {
	t.Parallel()
	if !slices.Contains(roles[RoleOperator].permissions, PermDiagnosticsLogs) {
		t.Fatal("operator must list diagnostics.logs explicitly in the roles table")
	}
	if slices.Contains(roles[RoleOperator].permissions, PermBaselineManage) {
		t.Fatal("operator must not get baseline.manage")
	}
}

// Пользователь по-прежнему только читает: новые права его не расширяют.
func TestUserStaysReadOnlyWithOpsPermissions(t *testing.T) {
	t.Parallel()
	if got := RoleUser.Permissions(); !slices.Equal(got, []Permission{PermRead}) {
		t.Fatalf("user permissions = %v, want [read]", got)
	}
}

func TestOpsPermissionsExposedInRolesList(t *testing.T) {
	t.Parallel()
	byRole := map[Role][]Permission{}
	for _, ri := range Roles() {
		byRole[ri.ID] = ri.Permissions
	}
	if !slices.Contains(byRole[RoleAdmin], PermBaselineManage) || !slices.Contains(byRole[RoleAdmin], PermDiagnosticsLogs) {
		t.Errorf("admin role list lacks ops permissions: %v", byRole[RoleAdmin])
	}
	if !slices.Contains(byRole[RoleOperator], PermDiagnosticsLogs) {
		t.Errorf("operator role list lacks diagnostics.logs: %v", byRole[RoleOperator])
	}
}
