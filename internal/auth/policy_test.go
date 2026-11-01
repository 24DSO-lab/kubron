// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Эти тесты — страховка от главной ошибки ролевой модели: добавить новый Wails-метод и забыть
// его защитить. Они разбирают исходники internal/app/*.go и проверяют, что
//   - каждый экспортируемый метод App начинается с проверки права a.authz(...);
//   - изменяющие методы не защищены «слабым» правом чтения;
//   - особые методы (терминал, секреты, кластеры, пользователи) требуют именно своих прав.

// Методы без authz: до входа в систему они обязаны быть доступны.
var publicMethods = map[string]bool{
	"AuthStatus":     true,
	"SetupAdmin":     true,
	"Login":          true,
	"Logout":         true,
	"CurrentUser":    true,
	"ChangePassword": true,
	"ReportActivity": true,
	// Возможности оболочки нужны экрану входа, до сессии.
	"GetCapabilities": true,
}

// Точные требования для особых методов.
var exactPermission = map[string]Permission{
	"AddHelmRepo":             PermClusterManage,
	"RemoveHelmRepo":          PermClusterManage,
	"UpdateHelmRepos":         PermClusterManage,
	"ListHelmRepos":           PermRead,
	"SearchHelmCharts":        PermRead,
	"GetHelmChartVersions":    PermRead,
	"GetHelmChartPackage":     PermRead,
	"PreviewHelmInstall":      PermWrite,
	"InstallHelmChart":        PermWrite,
	"PreviewHelmChartUpgrade": PermSecretsRead,
	"GetHelmUpgradeSources":   PermRead,
	"UpgradeHelmChart":        PermWrite,
	"PreviewHelmUpgrade":      PermSecretsRead,
	"UpgradeHelmRelease":      PermWrite,
	"RollbackHelmRelease":     PermOperate,
	"UninstallHelmRelease":    PermWrite,
	"GetHelmRevisionDiff":     PermSecretsRead,
	"GetSecretValue":          PermSecretsRead,
	"DetectShell":             PermExec,
	"StartTerminalSession":    PermExec,
	"WriteTerminal":           PermExec,
	"GetCertificateExpiry":    PermSecretsRead,
	"ResizeTerminal":          PermExec,
	"CloseTerminal":           PermExec,
	"StartPortForward":        PermPortForward,
	"StartServicePortForward": PermPortForward,
	"StopPortForward":         PermPortForward,
	"GetPortForwardURL":       PermPortForward,
	"PickKubeconfigFile":      PermClusterManage,
	"AddClusterFromFile":      PermClusterManage,
	"AddClusterFromText":      PermClusterManage,
	"UpdateCluster":           PermClusterManage,
	"InspectKubeconfig":       PermClusterManage,
	"TestKubeconfig":          PermClusterManage,
	"GetClusterKubeconfig":    PermClusterManage,
	"DeleteCluster":           PermClusterManage,
	"ListRoles":               PermUsersManage,
	"ListUsers":               PermUsersManage,
	"CreateUser":              PermUsersManage,
	"UpdateUserRole":          PermUsersManage,
	"SetUserDisabled":         PermUsersManage,
	"ResetUserPassword":       PermUsersManage,
	"DeleteUser":              PermUsersManage,
	"ApplyYAML":               PermWrite,
	"CreateResourceYAML":      PermWrite,
	"ScaleDeployment":         PermOperate,
	"RestartDeployment":       PermOperate,
	"RollbackDeployment":      PermOperate,
	"ScaleStatefulSet":        PermOperate,
	"RestartStatefulSet":      PermOperate,
	"ScaleReplicaSet":         PermOperate,
	"RestartDaemonSet":        PermOperate,
	"RetryJob":                PermOperate,
	"GetAuditLog":             PermAuditRead,
	"ExportAuditLog":          PermAuditRead,
	"SetIdleTimeout":          PermUsersManage,
	"CordonNode":              PermOperate,
	"DrainNode":               PermWrite,
	"StartNodeShell":          PermExec,
	"SetDefaultIngressClass":  PermWrite,
	"SyncArgoApplication":     PermOperate,
	"RefreshArgoApplication":  PermOperate,
	"TerminateArgoOperation":  PermOperate,
	"RollbackArgoApplication": PermOperate,
	"OpenArgoUI":              PermPortForward,
	"SetArgoAPIToken":         PermClusterManage,
	"GenerateArgoAPIToken":    PermClusterManage,
	"DeleteArgoAPIToken":      PermWrite,
	"GetArgoApplicationDiff":  PermPortForward,
	"SuspendArgoAutoSync":     PermWrite,
	"ResumeArgoAutoSync":      PermWrite,
	"DeleteArgoApplication":   PermWrite,
	"StartLogStream":          PermRead,
	"StopLogStream":           PermRead,
	"GetHelmReleaseContent":   PermSecretsRead,
	"CancelHelmOperation":     PermWrite,
	"ListHelmOperations":      PermRead,
	"GetHelmOperation":        PermRead,
	"ClearHelmOperations":     PermClusterManage,
	"AddHelmOCIRepo":          PermClusterManage,
	"AddHelmOCIChart":         PermClusterManage,
	"RemoveHelmOCIChart":      PermClusterManage,
	// Команды (этап 3): состав — администратор команды или организации (users.manage), кластеры — clusters.manage.
	"ListTeams":                 PermRead,
	"CreateTeam":                PermUsersManage,
	"RenameTeam":                PermUsersManage,
	"DeleteTeam":                PermUsersManage,
	"ListTeamMembers":           PermUsersManage,
	"SetTeamMember":             PermUsersManage,
	"RemoveTeamMember":          PermUsersManage,
	"AddClusterToTeam":          PermClusterManage,
	"AddOIDCClusterToTeam":      PermClusterManage,
	"AddInClusterToTeam":        PermClusterManage,
	"SetClusterOIDCCredentials": PermClusterManage,
	"MoveCluster":               PermClusterManage,
}

// Глаголы, по которым метод считается изменяющим: слабое право чтения для них недопустимо.
var mutatingPrefixes = []string{
	"Delete", "Scale", "Restart", "Rollback", "Retry", "Evict", "Apply",
	"Install", "Uninstall", "Add", "Update", "Create", "Reset", "Start", "Stop",
	"Exec", "Write", "Resize", "Close",

	"Cordon", "Drain",
	"Sync", "Refresh", "Terminate", "Generate",
	"Suspend", "Resume",
	"Set", "Remove", "Move", "Rename",
}

// Изменяющие по имени методы, которым чтения достаточно (с обоснованием).
var mutatingButReadOK = map[string]string{
	"SaveAppSettings": "theme and refresh interval are personal UI settings, not cluster resources",
}

var permByIdent = map[string]Permission{
	"PermRead":          PermRead,
	"PermSecretsRead":   PermSecretsRead,
	"PermOperate":       PermOperate,
	"PermWrite":         PermWrite,
	"PermExec":          PermExec,
	"PermPortForward":   PermPortForward,
	"PermClusterManage": PermClusterManage,
	"PermUsersManage":   PermUsersManage,
	"PermAuditRead":     PermAuditRead,
}

type methodGuard struct {
	name string
	perm Permission
	has  bool
}

func parseAppMethods(t *testing.T) []methodGuard {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "app", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("internal/app/*.go files not found: %v", err)
	}
	fset := token.NewFileSet()
	var out []methodGuard
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || !isAppReceiver(fn.Recv) {
				continue
			}
			g := methodGuard{name: fn.Name.Name}
			if fn.Body != nil && len(fn.Body.List) > 0 {
				g.perm, g.has = firstStmtPermission(fn.Body.List[0], t, fn.Name.Name)
			}
			out = append(out, g)
		}
	}
	return out
}

func isAppReceiver(r *ast.FieldList) bool {
	if len(r.List) != 1 {
		return false
	}
	typ := r.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	id, ok := typ.(*ast.Ident)
	return ok && id.Name == "App"
}

// firstStmtPermission ищет в первом операторе вызов a.authz(auth.PermX[, ключи и значения для журнала]).
func firstStmtPermission(stmt ast.Stmt, t *testing.T, method string) (Permission, bool) {
	ifs, ok := stmt.(*ast.IfStmt)
	if !ok {
		return "", false
	}
	var found Permission
	var has bool
	check := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "authz" && sel.Sel.Name != "authzCluster" && sel.Sel.Name != "authzTeam" && sel.Sel.Name != "authzAny") || len(call.Args) < 1 {
				return true
			}
			arg, ok := call.Args[0].(*ast.SelectorExpr)
			if !ok {
				t.Errorf("%s: the authz argument must be an auth.Perm* constant", method)
				return true
			}
			p, known := permByIdent[arg.Sel.Name]
			if !known {
				t.Errorf("%s: unknown permission %s (add it to the test's permByIdent)", method, arg.Sel.Name)
				return true
			}
			found, has = p, true
			return false
		})
	}
	if ifs.Init != nil {
		check(ifs.Init)
	}
	check(ifs.Cond)
	return found, has
}

func TestEveryAppMethodIsGuarded(t *testing.T) {
	methods := parseAppMethods(t)
	if len(methods) < 100 {
		t.Fatalf("suspiciously few App methods: %d (is the parser broken?)", len(methods))
	}
	for _, m := range methods {
		if publicMethods[m.name] {
			if m.has {
				t.Errorf("%s: a public method must not call authz", m.name)
			}
			continue
		}
		if !m.has {
			t.Errorf("%s: no permission check as the first statement (if err := a.authz(auth.PermX); err != nil {...})", m.name)
		}
	}
}

func TestMethodPermissionsMatchPolicy(t *testing.T) {
	for _, m := range parseAppMethods(t) {
		if publicMethods[m.name] || !m.has {
			continue
		}
		if want, ok := exactPermission[m.name]; ok {
			if m.perm != want {
				t.Errorf("%s: requires %q, expected %q", m.name, m.perm, want)
			}
			continue
		}
		if _, ok := mutatingButReadOK[m.name]; ok {
			continue
		}
		weak := m.perm == PermRead || m.perm == PermSecretsRead
		if !weak {
			continue
		}
		for _, p := range mutatingPrefixes {
			if strings.HasPrefix(m.name, p) {
				t.Errorf("%s: the name looks like a mutating operation but is protected by %q; "+
					"use PermWrite (or add the method to exactPermission / mutatingButReadOK)", m.name, m.perm)
				break
			}
		}
	}
}

// Удаление, выселение и произвольный YAML — только с PermWrite (или более строгим): право operate,
// выданное оператору, не должно «просочиться» в разрушительные методы.
func TestDestructiveMethodsRequireWrite(t *testing.T) {
	for _, m := range parseAppMethods(t) {
		if !m.has {
			continue
		}

		destructive := strings.HasPrefix(m.name, "Delete") || strings.HasPrefix(m.name, "Evict") || strings.HasPrefix(m.name, "Drain") || m.name == "ApplyYAML" || m.name == "CreateResourceYAML"
		if !destructive || m.name == "DeleteCluster" || m.name == "DeleteUser" || m.name == "DeleteTeam" {
			continue // у этих особые права из exactPermission
		}
		if m.perm != PermWrite {
			t.Errorf("%s: a destructive method requires %q, expected %q", m.name, m.perm, PermWrite)
		}
	}
}

func TestExactPermissionTableHasNoStaleEntries(t *testing.T) {
	have := map[string]bool{}
	for _, m := range parseAppMethods(t) {
		have[m.name] = true
	}
	var stale []string
	for name := range exactPermission {
		if !have[name] {
			stale = append(stale, name)
		}
	}
	for name := range publicMethods {
		if !have[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("the test tables contain methods that do not exist in App: %v", stale)
	}
}

func TestPermIdentTableCoversAllPermissions(t *testing.T) {
	covered := map[Permission]bool{}
	for _, p := range permByIdent {
		covered[p] = true
	}
	for _, p := range allPermissions {
		if !covered[p] {
			t.Errorf("permission %q is missing from the test's permByIdent", p)
		}
	}
}
