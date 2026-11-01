// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/websession"
	"github.com/24DSO-labs/kubron/pkg/ext"
)

func sessHash(s string) websession.Hash { return sha256.Sum256([]byte(s)) }

func TestWebSessionsLifecycle(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.Users().Save(&ext.UserData{Users: []ext.UserRecord{
		{ID: "u1", Username: "a", Role: "admin", PasswordHash: "h", CreatedAt: now, UpdatedAt: now},
		{ID: "u2", Username: "b", Role: "user", PasswordHash: "h", CreatedAt: now, UpdatedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}
	saveCluster(t, s, "c1", "One", []byte("kc"))
	w := s.Sessions()

	rec := websession.Record{TokenHash: sessHash("t1"), ID: "s1", UserID: "u1", CSRF: "csrf", CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), LastActive: now, Source: "10.0.0.1", ReplicaID: "r-a"}
	if err := w.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := w.Get(ctx, sessHash("t1"))
	if err != nil || got.ID != "s1" || got.UserID != "u1" || got.CSRF != "csrf" || got.Source != "10.0.0.1" ||
		got.ReplicaID != "r-a" || got.ActiveCluster != "" || got.TokenHash != sessHash("t1") || !got.ExpiresAt.Equal(rec.ExpiresAt) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := w.Get(ctx, sessHash("unknown")); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("unknown token: %v", err)
	}

	// активность не уменьшается; активный кластер запоминается, неизвестный не ломает сессию
	if err := w.Touch(ctx, "s1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := w.Touch(ctx, "s1", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := w.SetActiveCluster(ctx, "s1", "c1"); err != nil {
		t.Fatal(err)
	}
	got, _ = w.Get(ctx, sessHash("t1"))
	if !got.LastActive.Equal(now.Add(time.Minute)) || got.ActiveCluster != "c1" {
		t.Errorf("touch/cluster: %+v", got)
	}
	if err := w.SetActiveCluster(ctx, "s1", "ghost"); err != nil {
		t.Fatalf("an unknown cluster is not an error: %v", err)
	}
	if got, _ = w.Get(ctx, sessHash("t1")); got.ActiveCluster != "" {
		t.Errorf("an unknown cluster must not be remembered: %q", got.ActiveCluster)
	}
	_ = w.SetActiveCluster(ctx, "s1", "c1")

	// ротация: старый токен действует до prevValidUntil, id сессии прежний
	if err := w.Rotate(ctx, "s1", sessHash("t2"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"t1", "t2"} {
		if got, err := w.Get(ctx, sessHash(tok)); err != nil || got.ID != "s1" || got.TokenHash != sessHash("t2") {
			t.Errorf("token %s after rotation: %+v %v", tok, got, err)
		}
	}
	if err := w.Rotate(ctx, "s1", sessHash("t3"), time.Now().Add(-time.Second)); err != nil { // прежний (t2) уже недействителен
		t.Fatal(err)
	}
	if _, err := w.Get(ctx, sessHash("t2")); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("the previous token must stop working after its grace period: %v", err)
	}
	if _, err := w.Get(ctx, sessHash("t3")); err != nil {
		t.Errorf("the current token: %v", err)
	}
	if err := w.Rotate(ctx, "nope", sessHash("x"), time.Now()); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("rotating an unknown session: %v", err)
	}

	// удаление кластера сбрасывает активный кластер, но не сессию
	if _, err := s.pool.Exec(ctx, `DELETE FROM clusters WHERE id = 'c1'`); err != nil {
		t.Fatal(err)
	}
	if got, err = w.Get(ctx, sessHash("t3")); err != nil || got.ActiveCluster != "" {
		t.Errorf("after the cluster is deleted: %+v %v", got, err)
	}
}

func TestWebSessionsExpiryDisabledUsersAndRevocation(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	save := func(disabled bool) {
		t.Helper()
		if err := s.Users().Save(&ext.UserData{Users: []ext.UserRecord{
			{ID: "u1", Username: "a", Role: "admin", PasswordHash: "h", CreatedAt: now, UpdatedAt: now, Disabled: disabled},
			{ID: "u2", Username: "b", Role: "user", PasswordHash: "h", CreatedAt: now, UpdatedAt: now},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	save(false)
	w := s.Sessions()
	mk := func(tok, id, user string, expires time.Time) {
		t.Helper()
		if err := w.Create(ctx, websession.Record{TokenHash: sessHash(tok), ID: id, UserID: user, CSRF: "c",
			CreatedAt: now, ExpiresAt: expires, LastActive: now}); err != nil {
			t.Fatal(err)
		}
	}
	mk("a1", "sa1", "u1", now.Add(time.Hour))
	mk("a2", "sa2", "u1", now.Add(time.Hour))
	mk("b1", "sb1", "u2", now.Add(time.Hour))
	mk("old", "sold", "u2", now.Add(-time.Minute))

	if _, err := w.Get(ctx, sessHash("old")); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("an expired session must not be returned: %v", err)
	}
	if n, err := w.DeleteExpired(ctx); err != nil || n != 1 {
		t.Errorf("DeleteExpired: %d %v", n, err)
	}

	// отключённый пользователь: сессия недействительна сразу, без события
	save(true)
	if _, err := w.Get(ctx, sessHash("a1")); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("a disabled user's session must be rejected: %v", err)
	}
	if _, err := w.Get(ctx, sessHash("b1")); err != nil {
		t.Errorf("other users are not affected: %v", err)
	}
	save(false)

	// смена пароля: все сессии пользователя, кроме текущей
	ids, err := w.DeleteUser(ctx, "u1", "sa1")
	if err != nil || len(ids) != 1 || ids[0] != "sa2" {
		t.Fatalf("DeleteUser: %v %v", ids, err)
	}
	if _, err := w.Get(ctx, sessHash("a1")); err != nil {
		t.Errorf("the current session must survive: %v", err)
	}
	if _, err := w.Get(ctx, sessHash("a2")); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("the other session must end: %v", err)
	}
	if err := w.Delete(ctx, "sa1"); err != nil {
		t.Fatal(err)
	}
	if err := w.Delete(ctx, "sa1"); err != nil {
		t.Errorf("Delete is idempotent: %v", err)
	}

	// удаление пользователя удаляет его сессии (каскад)
	if err := s.Users().Save(&ext.UserData{Users: []ext.UserRecord{
		{ID: "u1", Username: "a", Role: "admin", PasswordHash: "h", CreatedAt: now, UpdatedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Get(ctx, sessHash("b1")); !errors.Is(err, websession.ErrNotFound) {
		t.Errorf("sessions of a deleted user must go: %v", err)
	}
}
