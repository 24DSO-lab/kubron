// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

// memUsers — хранилище пользователей «из другого модуля»: знает только публичные типы pkg/ext.
type memUsers struct{ d *ext.UserData }

func (m *memUsers) Load() (*ext.UserData, error) {
	if m.d == nil {
		return &ext.UserData{}, nil
	}
	return m.d, nil
}
func (m *memUsers) Save(d *ext.UserData) error { m.d = d; return nil }

// Service должен работать поверх внешнего хранилища: создать администратора, перезапуститься и войти.
func TestServiceOverExternalBackend(t *testing.T) {
	mem := &memUsers{}
	svc, err := NewServiceWithBackend(FromExt(mem))
	if err != nil {
		t.Fatal(err)
	}
	if !svc.Status().NeedsSetup {
		t.Fatal("an empty store must require initial setup")
	}
	if _, err := svc.Setup("admin", "correct-horse-1"); err != nil {
		t.Fatal(err)
	}
	if mem.d == nil || len(mem.d.Users) != 1 || mem.d.Users[0].Role != string(RoleAdmin) {
		t.Fatalf("the external store did not receive an administrator: %+v", mem.d)
	}

	// «Перезапуск»: новый сервис над тем же хранилищем.
	svc2, err := NewServiceWithBackend(FromExt(mem))
	if err != nil {
		t.Fatal(err)
	}
	if svc2.Status().NeedsSetup {
		t.Fatal("no setup is needed after a restart")
	}
	if _, err := svc2.Login("admin", "correct-horse-1"); err != nil {
		t.Fatalf("login after a restart: %v", err)
	}
}

func TestExtAdapterKeepsAllFields(t *testing.T) {
	idle := 15
	now := time.Now().UTC().Truncate(time.Second)
	in := &Data{
		Users: []UserRecord{{
			ID: "1", Username: "u", Role: RoleAdmin, PasswordHash: "h", Disabled: true,
			MustChangePassword: true, CreatedAt: now, UpdatedAt: now, LastLoginAt: &now,
		}},
		Settings: SecuritySettings{IdleTimeoutMinutes: &idle},
	}
	mem := &memUsers{}
	b := FromExt(mem)
	if err := b.Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	u := out.Users[0]
	if u != in.Users[0] && (u.ID != "1" || u.Role != RoleAdmin || !u.Disabled || !u.MustChangePassword ||
		u.LastLoginAt == nil || !u.LastLoginAt.Equal(now) || *out.Settings.IdleTimeoutMinutes != 15) {
		t.Fatalf("fields were lost: %+v", out)
	}
}
