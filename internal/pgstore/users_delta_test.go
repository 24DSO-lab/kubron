// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

func userIDs(t *testing.T, s *Store) map[string]ext.UserRecord {
	t.Helper()
	d, err := s.Users().Load()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ext.UserRecord{}
	for _, u := range d.Users {
		out[u.ID] = u
	}
	return out
}

func TestUserDeltaDoesNotTouchOtherUsers(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	d := s.UserDelta()
	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(id, name, role string) ext.UserRecord {
		return ext.UserRecord{ID: id, Username: name, Role: role, PasswordHash: "h-" + id, CreatedAt: now, UpdatedAt: now}
	}
	// «Реплика A» и «реплика B» создают по пользователю, не зная друг о друге: Save затёр бы одного из них.
	if err := d.UpsertUser(mk("u1", "alice", "admin")); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertUser(mk("u2", "bob", "user")); err != nil {
		t.Fatal(err)
	}
	if got := userIDs(t, s); len(got) != 2 {
		t.Fatalf("both users must stay: %+v", got)
	}

	// изменение роли обновляет и членство в команде по умолчанию (как Save)
	upd := mk("u2", "bob", "operator")
	if err := d.UpsertUser(upd); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := s.pool.QueryRow(context.Background(), `SELECT role FROM memberships WHERE user_id = 'u2' AND team_id = $1`, s.teamID).Scan(&role); err != nil || role != "operator" {
		t.Errorf("membership role: %q %v", role, err)
	}

	// вход меняет время и хэш, но не роль, выставленную в это время другой репликой
	if err := d.UpsertUser(mk("u2", "bob", "admin")); err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Minute)
	if err := d.TouchLogin("u2", at, "rehash"); err != nil {
		t.Fatal(err)
	}
	got := userIDs(t, s)["u2"]
	if got.Role != "admin" || got.PasswordHash != "rehash" || got.LastLoginAt == nil || !got.LastLoginAt.Equal(at) {
		t.Errorf("TouchLogin: %+v", got)
	}
	if err := d.TouchLogin("u2", at.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if got = userIDs(t, s)["u2"]; got.PasswordHash != "rehash" {
		t.Errorf("an empty hash must keep the old one: %q", got.PasswordHash)
	}

	// имя занято другим пользователем
	if err := d.UpsertUser(mk("u3", "ALICE", "user")); !errors.Is(err, ErrConflict) && err == nil {
		t.Errorf("a taken name must be a conflict: %v", err)
	}
	if err := d.UpsertUser(ext.UserRecord{ID: "u4", Username: "x", Role: "root"}); err == nil {
		t.Error("an unknown role must be rejected")
	}

	// удаление одного не затрагивает остальных; повторное — не ошибка
	if err := d.DeleteUser("u1"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteUser("u1"); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if got := userIDs(t, s); len(got) != 1 || got["u2"].Username != "bob" {
		t.Errorf("after delete: %+v", got)
	}

	// настройки
	idle := 7
	if err := d.SaveSettings(ext.SecuritySettings{IdleTimeoutMinutes: &idle}); err != nil {
		t.Fatal(err)
	}
	data, _ := s.Users().Load()
	if data.Settings.IdleTimeoutMinutes == nil || *data.Settings.IdleTimeoutMinutes != 7 {
		t.Errorf("settings: %+v", data.Settings)
	}
}
