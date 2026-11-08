// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/store"
)

const adminPass = "correct horse battery"

func noEnv(string) string { return "" }

func envWith(pass string) func(string) string {
	return func(k string) string {
		if k == adminPasswordEnv {
			return pass
		}
		return ""
	}
}

func TestCreateAdminFromEnvironmentCreatesAdminAndAudits(t *testing.T) {
	st := store.NewFile(t.TempDir())
	var out bytes.Buffer
	if err := runCreateAdmin([]string{"--username", "root"}, envWith(adminPass), nil, &out, st); err != nil {
		t.Fatalf("create-admin: %v", err)
	}
	if strings.Contains(out.String(), adminPass) {
		t.Fatal("the password must never be printed")
	}
	svc, err := auth.NewServiceWithBackend(st.Users())
	if err != nil {
		t.Fatal(err)
	}
	if svc.Status().NeedsSetup {
		t.Fatal("setup must be complete")
	}
	u, err := svc.Login(t.Context(), "root", adminPass)
	if err != nil || u.Role != auth.RoleAdmin {
		t.Fatalf("login as the new administrator: %+v %v", u, err)
	}
	page, err := audit.New(st.Audit()).Query(audit.Filter{Action: "Setup"})
	if err != nil || page == nil || len(page.Events) != 1 || page.Events[0].User != "root" || page.Events[0].Result != audit.ResultOK {
		t.Fatalf("the audit must hold one successful Setup: %+v %v", page, err)
	}
	if page.Events[0].Source != "cli" {
		t.Errorf("source = %q, want cli", page.Events[0].Source)
	}
}

func TestCreateAdminReadsPasswordFromStdin(t *testing.T) {
	for _, in := range []string{adminPass + "\n", adminPass + "\r\n", adminPass} {
		dir := store.NewFile(t.TempDir())
		if err := runCreateAdmin([]string{"--username", "root"}, noEnv, strings.NewReader(in), &bytes.Buffer{}, dir); err != nil {
			t.Fatalf("stdin %q: %v", in, err)
		}
		svc, _ := auth.NewServiceWithBackend(dir.Users())
		if _, err := svc.Login(t.Context(), "root", adminPass); err != nil {
			t.Fatalf("the password from stdin %q must be exact: %v", in, err)
		}
	}
}

func TestCreateAdminPasswordSources(t *testing.T) {
	if _, err := readAdminPassword(noEnv, strings.NewReader("")); err == nil {
		t.Error("empty stdin and no environment must be an error")
	}
	if _, err := readAdminPassword(noEnv, strings.NewReader("\n")); err == nil {
		t.Error("an empty line must be an error")
	}
	if _, err := readAdminPassword(noEnv, nil); err == nil {
		t.Error("no source must be an error")
	}
	if p, _ := readAdminPassword(envWith("from-env"), strings.NewReader("from-stdin\n")); p != "from-env" {
		t.Errorf("the environment wins over stdin: %q", p)
	}
	long := strings.Repeat("x", 3*maxPasswordInput)
	if p, _ := readAdminPassword(noEnv, strings.NewReader(long+"\n")); len(p) > maxPasswordInput {
		t.Errorf("stdin is limited to %d bytes, got %d", maxPasswordInput, len(p))
	}
}

func TestCreateAdminRefusesWhenUsersExist(t *testing.T) {
	st := store.NewFile(t.TempDir())
	if err := runCreateAdmin([]string{"--username", "root"}, envWith(adminPass), nil, &bytes.Buffer{}, st); err != nil {
		t.Fatal(err)
	}
	err := runCreateAdmin([]string{"--username", "second"}, envWith(adminPass), nil, &bytes.Buffer{}, st)
	if !errors.Is(err, auth.ErrSetupDone) {
		t.Fatalf("second create-admin: %v, want ErrSetupDone", err)
	}
	svc, _ := auth.NewServiceWithBackend(st.Users())
	if _, err := svc.Login(t.Context(), "second", adminPass); err == nil {
		t.Fatal("the refused administrator must not exist")
	}
}

func TestCreateAdminValidatesCredentialsLikeSetupAdmin(t *testing.T) {
	cases := map[string][]string{
		"short password": {"--username", "root"},
		"bad username":   {"--username", "a"},
	}
	pass := map[string]string{"short password": "short", "bad username": adminPass}
	for name, args := range cases {
		st := store.NewFile(t.TempDir())
		if err := runCreateAdmin(args, envWith(pass[name]), nil, &bytes.Buffer{}, st); err == nil {
			t.Errorf("%s must be rejected", name)
		}
		svc, _ := auth.NewServiceWithBackend(st.Users())
		if !svc.Status().NeedsSetup {
			t.Errorf("%s: no user may be created", name)
		}
	}
	// пароль не может совпадать с логином
	st := store.NewFile(t.TempDir())
	if err := runCreateAdmin([]string{"--username", "samename1"}, envWith("samename1"), nil, &bytes.Buffer{}, st); !errors.Is(err, auth.ErrWeakPassword) {
		t.Errorf("password equal to the username: %v", err)
	}
}

func TestCreateAdminArguments(t *testing.T) {
	st := store.NewFile(t.TempDir())
	if err := runCreateAdmin(nil, envWith(adminPass), nil, &bytes.Buffer{}, st); err == nil {
		t.Error("--username is required")
	}
	// Пароль аргументом командной строки не принимается ни в каком виде
	if err := runCreateAdmin([]string{"--username", "root", adminPass}, envWith(adminPass), nil, &bytes.Buffer{}, st); err == nil {
		t.Error("a positional password must be rejected")
	}
	if err := runCreateAdmin([]string{"--username", "root", "--password", adminPass}, envWith(adminPass), nil, &bytes.Buffer{}, st); err == nil {
		t.Error("a --password flag must not exist")
	}
	svc, _ := auth.NewServiceWithBackend(st.Users())
	if !svc.Status().NeedsSetup {
		t.Error("no user may be created after rejected invocations")
	}
}

func TestCreateAdminIsRegisteredAsAdminCommand(t *testing.T) {
	if !adminCommands["create-admin"] {
		t.Fatal("create-admin must be a service command")
	}
}
