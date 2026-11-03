// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	argon2Memory = 64 // тесты не должны ждать полноценного хеширования
	argon2Time = 1
}

const goodPass = "correct horse"

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewService(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

// setupWithUser: администратор admin + обычный пользователь bob (пароль уже сменён).
func setupWithUser(t *testing.T) (*Service, *UserInfo) {
	t.Helper()
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	bob, err := s.CreateUser("bob", "temp-pass-1", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	s.Logout()
	if _, err := s.Login("bob", "temp-pass-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword("temp-pass-1", "bobs-own-pass"); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	return s, bob
}

func loginAdmin(t *testing.T, s *Service) {
	t.Helper()
	s.Logout()
	if _, err := s.Login("admin", goodPass); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunNeedsSetup(t *testing.T) {
	s, _ := newTestService(t)
	st := s.Status()
	if !st.NeedsSetup || st.User != nil {
		t.Fatalf("expected initial setup, got %+v", st)
	}
	if _, err := s.Login("admin", goodPass); !errors.Is(err, ErrSetupRequired) {
		t.Fatalf("login before setup: %v", err)
	}
	if err := s.Authorize(PermRead); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("access must be denied without login, got %v", err)
	}
}

func TestSetupCreatesAdminOnlyOnce(t *testing.T) {
	s, _ := newTestService(t)
	u, err := s.Setup("admin", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleAdmin || u.MustChangePassword {
		t.Fatalf("wrong first user: %+v", u)
	}
	if err := s.Authorize(PermUsersManage); err != nil {
		t.Fatalf("the admin must be logged in after setup: %v", err)
	}
	if _, err := s.Setup("evil", goodPass); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("repeated setup must be forbidden: %v", err)
	}
}

func TestSetupValidation(t *testing.T) {
	s, _ := newTestService(t)
	cases := []struct{ user, pass string }{
		{"ab", goodPass},                   // короткий логин
		{"bad name", goodPass},             // пробел
		{"admin", "short"},                 // короткий пароль
		{"admin", "ADMIN"},                 // слишком короткий
		{"administrator", "ADMINISTRATOR"}, // совпадает с логином
	}
	for _, c := range cases {
		if _, err := s.Setup(c.user, c.pass); err == nil {
			t.Errorf("Setup(%q, %q) should have returned an error", c.user, c.pass)
		}
	}
	if !s.Status().NeedsSetup {
		t.Error("failed attempts must not create users")
	}
}

func TestLoginLogout(t *testing.T) {
	s, _ := setupWithUser(t)
	if _, err := s.Login("BOB", "bobs-own-pass"); err != nil { // логин регистронезависим
		t.Fatalf("login: %v", err)
	}
	if err := s.Authorize(PermRead); err != nil {
		t.Errorf("read: %v", err)
	}
	s.Logout()
	if err := s.Authorize(PermRead); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("access must be closed after logout, got %v", err)
	}
}

func TestLoginErrorsDoNotRevealUser(t *testing.T) {
	s, _ := setupWithUser(t)
	_, e1 := s.Login("bob", "wrong-password")
	_, e2 := s.Login("nobody", "wrong-password")
	if !errors.Is(e1, ErrInvalidCredentials) || !errors.Is(e2, ErrInvalidCredentials) {
		t.Fatalf("expected the same error: %v / %v", e1, e2)
	}
}

func TestUserRoleEnforcement(t *testing.T) {
	s, _ := setupWithUser(t)
	if _, err := s.Login("bob", "bobs-own-pass"); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(PermRead); err != nil {
		t.Errorf("user must have %q: %v", PermRead, err)
	}
	for _, p := range []Permission{PermSecretsRead, PermWrite, PermExec, PermPortForward, PermClusterManage, PermUsersManage} {
		if err := s.Authorize(p); !errors.Is(err, ErrForbidden) {
			t.Errorf("user must not have %q, got %v", p, err)
		}
	}
	if _, err := s.ListUsers(); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListUsers for user: %v", err)
	}
	if _, err := s.CreateUser("eve", goodPass, RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateUser for user: %v", err)
	}
}

func TestAdminCanDoEverything(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	for _, p := range allPermissions {
		if err := s.Authorize(p); err != nil {
			t.Errorf("admin must have %q: %v", p, err)
		}
	}
}

func TestNewUserMustChangePassword(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("bob", "temp-pass-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	u, err := s.Login("bob", "temp-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if !u.MustChangePassword {
		t.Fatal("a new user must change the temporary password")
	}
	if err := s.Authorize(PermRead); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("access is denied before the password change: %v", err)
	}
	if err := s.ChangePassword("temp-pass-1", "temp-pass-1"); err == nil {
		t.Fatal("the new password must not match the old one")
	}
	if err := s.ChangePassword("nope-nope-1", "fresh-pass-1"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong current password: %v", err)
	}
	if err := s.ChangePassword("temp-pass-1", "fresh-pass-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(PermRead); err != nil {
		t.Fatalf("access is open after the password change: %v", err)
	}
	s.Logout()
	if _, err := s.Login("bob", "temp-pass-1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("the old password must stop working")
	}
	if _, err := s.Login("bob", "fresh-pass-1"); err != nil {
		t.Fatal(err)
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	s, _ := setupWithUser(t)
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	for i := 0; i < freeAttempts; i++ {
		if _, err := s.Login("bob", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, err := s.Login("bob", "bobs-own-pass") // даже верный пароль во время блокировки
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("expected a lockout, got %v", err)
	}
	if !strings.Contains(locked.Error(), "try again in") {
		t.Errorf("error text: %q", locked.Error())
	}

	clock = clock.Add(baseLockout + time.Second)
	if _, err := s.Login("bob", "bobs-own-pass"); err != nil {
		t.Fatalf("login must work after the lockout ends: %v", err)
	}
	// успешный вход сбрасывает счётчик
	s.Logout()
	for i := 0; i < freeAttempts-1; i++ {
		_, _ = s.Login("bob", "wrong-password")
	}
	if _, err := s.Login("bob", "bobs-own-pass"); err != nil {
		t.Fatalf("the counter must have been reset: %v", err)
	}
}

func TestLastAdminProtection(t *testing.T) {
	s, _ := setupWithUser(t)
	loginAdmin(t, s)
	users, err := s.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	var adminID string
	for _, u := range users {
		if u.Username == "admin" {
			adminID = u.ID
		}
	}
	if _, err := s.UpdateRole(adminID, RoleUser); !errors.Is(err, ErrSelfAction) {
		t.Errorf("changing own role: %v", err)
	}
	if _, err := s.SetDisabled(adminID, true); !errors.Is(err, ErrSelfAction) {
		t.Errorf("disabling yourself: %v", err)
	}
	if err := s.DeleteUser(adminID); !errors.Is(err, ErrSelfAction) {
		t.Errorf("deleting yourself: %v", err)
	}
}

func TestPromoteThenDemoteAdmin(t *testing.T) {
	s, bob := setupWithUser(t)
	loginAdmin(t, s)
	if _, err := s.UpdateRole(bob.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	// теперь админов двое: второго можно разжаловать, а последнего (после этого) — нет
	if _, err := s.UpdateRole(bob.ID, RoleUser); err != nil {
		t.Fatalf("demotion with two admins: %v", err)
	}
	if _, err := s.UpdateRole(bob.ID, Role("root")); !errors.Is(err, ErrUnknownRole) {
		t.Errorf("unknown role: %v", err)
	}
}

func TestAdminCannotChangeOwnRoleEvenWithOtherAdmin(t *testing.T) {
	s, bob := setupWithUser(t)
	loginAdmin(t, s)
	admin, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRole(bob.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRole(admin.ID, RoleUser); !errors.Is(err, ErrSelfAction) {
		t.Errorf("self-demotion with two admins: %v", err)
	}
	// «смена» на ту же роль — не изменение, ошибки быть не должно
	if _, err := s.UpdateRole(admin.ID, RoleAdmin); err != nil {
		t.Errorf("same role: %v", err)
	}
}

func TestRoleChangeAppliesImmediately(t *testing.T) {
	s, bob := setupWithUser(t)
	// bob входит, затем админ (под другим процессом/сессией быть не может), поэтому моделируем напрямую
	if _, err := s.Login("bob", "bobs-own-pass"); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(PermWrite); !errors.Is(err, ErrForbidden) {
		t.Fatal("bob must not have write")
	}
	s.mu.Lock()
	s.st.findByID(bob.ID).Role = RoleAdmin
	s.mu.Unlock()
	if err := s.Authorize(PermWrite); err != nil {
		t.Errorf("the new role must apply without logging in again: %v", err)
	}
	s.mu.Lock()
	s.st.findByID(bob.ID).Disabled = true
	s.mu.Unlock()
	if err := s.Authorize(PermRead); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("a disabled user loses the session: %v", err)
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	s, bob := setupWithUser(t)
	loginAdmin(t, s)
	if _, err := s.SetDisabled(bob.ID, true); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	if _, err := s.Login("bob", "bobs-own-pass"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
	loginAdmin(t, s)
	if _, err := s.SetDisabled(bob.ID, false); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	if _, err := s.Login("bob", "bobs-own-pass"); err != nil {
		t.Fatalf("login is possible after enabling: %v", err)
	}
}

func TestAdminResetPassword(t *testing.T) {
	s, bob := setupWithUser(t)
	loginAdmin(t, s)
	if _, err := s.ResetPassword(bob.ID, "reset-pass-9"); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	if _, err := s.Login("bob", "bobs-own-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("the old password must be invalid")
	}
	u, err := s.Login("bob", "reset-pass-9")
	if err != nil || !u.MustChangePassword {
		t.Fatalf("a mandatory password change is required after reset: %v %+v", err, u)
	}
	loginAdmin(t, s)
	users, _ := s.ListUsers()
	for _, u := range users {
		if u.Username == "admin" {
			if _, err := s.ResetPassword(u.ID, "whatever-123"); !errors.Is(err, ErrSelfAction) {
				t.Errorf("resetting own password: %v", err)
			}
		}
	}
}

func TestDeleteUser(t *testing.T) {
	s, bob := setupWithUser(t)
	loginAdmin(t, s)
	if err := s.DeleteUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(bob.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("repeated delete: %v", err)
	}
	s.Logout()
	if _, err := s.Login("bob", "bobs-own-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("a deleted user cannot log in: %v", err)
	}
}

func TestDuplicateUsernameIsCaseInsensitive(t *testing.T) {
	s, _ := setupWithUser(t)
	loginAdmin(t, s)
	if _, err := s.CreateUser("BOB", goodPass, RoleUser); !errors.Is(err, ErrUserExists) {
		t.Fatalf("duplicate username: %v", err)
	}
	if _, err := s.CreateUser("eve", goodPass, Role("root")); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("unknown role: %v", err)
	}
}

func TestPersistenceAndFileSafety(t *testing.T) {
	s, dir := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, usersFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), goodPass) {
		t.Fatal("the password must not be stored in plain text")
	}
	if !strings.Contains(string(raw), hashScheme+"$") {
		t.Fatal("the file must contain a password hash")
	}
	if fi, err := os.Stat(path); err == nil && os.PathSeparator == '/' && fi.Mode().Perm() != 0o600 {
		t.Errorf("file permissions %v, expected 0600", fi.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}

	// новый экземпляр видит того же пользователя, но сессия не сохраняется
	s2, err := NewService(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := s2.Status()
	if st.NeedsSetup || st.User != nil {
		t.Fatalf("login is required after restart: %+v", st)
	}
	if _, err := s2.Login("admin", goodPass); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptFileDoesNotReopenSetup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, usersFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(dir); err == nil {
		t.Fatal("a corrupted file must cause an error, not an empty user list")
	}
}

func TestNewerFileVersionRejected(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(fileData{Version: storageVersion + 1})
	if err := os.WriteFile(filepath.Join(dir, usersFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(dir); err == nil {
		t.Fatal("a file from a newer version must be rejected")
	}
}

func TestUserInfoHasNoSecrets(t *testing.T) {
	s, _ := newTestService(t)
	u, err := s.Setup("admin", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(u)
	if strings.Contains(strings.ToLower(string(b)), "hash") || strings.Contains(string(b), goodPass) {
		t.Fatalf("UserInfo must not contain a hash or password: %s", b)
	}
}

func TestLogoutHook(t *testing.T) {
	s, _ := newTestService(t)
	calls := 0
	s.SetOnLogout(func() { calls++ })
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	s.Logout() // повторный выход без сессии не вызывает обработчик
	if calls != 1 {
		t.Fatalf("handler called %d times, expected 1", calls)
	}
}

func TestPasswordHashing(t *testing.T) {
	h, err := hashPassword(goodPass)
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := hashPassword(goodPass)
	if h == h2 {
		t.Error("the salt must be random")
	}
	if ok, _ := verifyPassword(goodPass, h); !ok {
		t.Error("the correct password failed verification")
	}
	if ok, _ := verifyPassword(goodPass+"x", h); ok {
		t.Error("a wrong password passed verification")
	}
	for _, bad := range []string{"", "x", "a$b$c$d", "pbkdf2-sha256$0$AA$AA", "pbkdf2-sha256$99999999999$AA$AA", "pbkdf2-sha256$1000$!!$!!"} {
		if ok, _ := verifyPassword(goodPass, bad); ok {
			t.Errorf("garbage hash %q was accepted", bad)
		}
	}
}

func TestRehashOnOutdatedParameters(t *testing.T) {
	oldMemory, oldTime := argon2Memory, argon2Time
	argon2Memory, argon2Time = 32, 1
	h, err := hashPassword(goodPass)
	if err != nil {
		t.Fatal(err)
	}
	argon2Memory, argon2Time = oldMemory, oldTime
	ok, rehash := verifyPassword(goodPass, h)
	if !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v: an outdated Argon2id hash must be rehashed", ok, rehash)
	}
	if _, rehash := verifyPassword(goodPass, mustHash(t, goodPass)); rehash {
		t.Error("a fresh hash must not be rehashed")
	}
}

func mustHash(t *testing.T, p string) string {
	t.Helper()
	h, err := hashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
