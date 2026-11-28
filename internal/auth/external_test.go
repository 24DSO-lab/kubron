// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func externalTestIdentity() ExternalIdentity {
	return ExternalIdentity{
		Issuer:   "https://id.example.test",
		Subject:  "subject-1",
		Username: "alice",
	}
}

func TestLoginExternalAutoCreatesAndReusesUser(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	s.Logout()

	policy := ExternalPolicy{AutoCreate: true, DefaultRole: RoleUser}
	first, err := s.LoginExternal(t.Context(), externalTestIdentity(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.Username != "alice" || first.Role != RoleUser || first.MustChangePassword {
		t.Fatalf("unexpected external user: %+v", first)
	}

	s.Logout()
	second, err := s.LoginExternal(t.Context(), externalTestIdentity(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("repeat sign-in created a second user: %s != %s", second.ID, first.ID)
	}
	if len(s.st.users) != 2 {
		t.Fatalf("expected admin and one external user, got %d", len(s.st.users))
	}
}

func TestLoginExternalLinksExistingLocalUser(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	local, err := s.CreateUser("alice", "temporary-password", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	s.Logout()

	got, err := s.LoginExternal(t.Context(), externalTestIdentity(), ExternalPolicy{
		LinkExisting: true,
		DefaultRole:  RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != local.ID {
		t.Fatalf("link created a new user: got %s want %s", got.ID, local.ID)
	}
	rec := s.st.findByID(local.ID)
	if rec.ExternalIssuer != "https://id.example.test" || rec.ExternalSubject != "subject-1" {
		t.Fatalf("external identity was not linked: %+v", rec)
	}
}

func TestLoginExternalProvisioningAndPasswordRules(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	s.Logout()

	if _, err := s.LoginExternal(t.Context(), externalTestIdentity(), ExternalPolicy{DefaultRole: RoleUser}); !errors.Is(err, ErrExternalNotProvisioned) {
		t.Fatalf("expected provisioning error, got %v", err)
	}
	u, err := s.LoginExternal(t.Context(), externalTestIdentity(), ExternalPolicy{
		AutoCreate:  true,
		DefaultRole: RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Logout()

	// The dummy hash is deliberately exercised too: it must never authenticate a passwordless account.
	if _, err := s.Login(t.Context(), u.Username, "kubron-dummy-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("external user without password hash logged in: %v", err)
	}
}

func TestLoginExternalDoesNotDemoteLastAdministrator(t *testing.T) {
	s, _ := newTestService(t)
	admin, err := s.Setup("admin", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	s.Logout()

	id := externalTestIdentity()
	id.Username = admin.Username
	id.Role = string(RoleUser)
	id.RoleMapped = true
	got, err := s.LoginExternal(t.Context(), id, ExternalPolicy{
		LinkExisting: true,
		DefaultRole:  RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleAdmin {
		t.Fatalf("last administrator was demoted: %s", got.Role)
	}
}

func TestConcurrentExternalLoginCreatesOneUser(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	s.Logout()

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.LoginExternal(t.Context(), externalTestIdentity(), ExternalPolicy{
				AutoCreate:  true,
				DefaultRole: RoleUser,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("external login: %v", err)
		}
	}

	count := 0
	for _, u := range s.st.users {
		if u.ExternalIssuer == "https://id.example.test" && u.ExternalSubject == "subject-1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one external user, got %d", count)
	}
}

func TestExternalIdentityFileRoundTripAndLegacyFile(t *testing.T) {
	s, dir := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	s.Logout()
	if _, err := s.LoginExternal(t.Context(), externalTestIdentity(), ExternalPolicy{
		AutoCreate:  true,
		DefaultRole: RoleUser,
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewService(dir)
	if err != nil {
		t.Fatal(err)
	}
	var persisted *UserRecord
	for i := range reopened.st.users {
		if reopened.st.users[i].ExternalIssuer == "https://id.example.test" {
			persisted = &reopened.st.users[i]
			break
		}
	}
	if persisted == nil || persisted.ExternalSubject != "subject-1" {
		t.Fatalf("external identity was not persisted: %+v", persisted)
	}

	legacyDir := t.TempDir()
	legacy := `{"version":1,"users":[{"id":"old","username":"legacy","role":"user","passwordHash":"hash","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}],"settings":{}}`
	if err := os.WriteFile(filepath.Join(legacyDir, usersFileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := NewService(legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	rec := old.st.findByID("old")
	if rec == nil || rec.ExternalIssuer != "" || rec.ExternalSubject != "" {
		t.Fatalf("legacy users.json did not load compatibly: %+v", rec)
	}
}
