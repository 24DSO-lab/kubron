// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testFree = 5
	testBase = 15 * time.Second
	testMax  = 5 * time.Minute
)

func TestLoginFailuresLockAfterFreeAttempts(t *testing.T) {
	f := newTestStore(t, keyString("k1")).LoginFailures()
	for i := 1; i < testFree; i++ {
		if err := f.RecordFailure(t.Context(), "alice", testFree, testBase, testMax); err != nil {
			t.Fatal(err)
		}
		if d, err := f.LockRemaining(t.Context(), "alice"); err != nil || d != 0 {
			t.Fatalf("attempt %d must stay free: %v %v", i, d, err)
		}
	}
	if err := f.RecordFailure(t.Context(), "alice", testFree, testBase, testMax); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.LockRemaining(t.Context(), "alice"); d < 10*time.Second || d > testBase {
		t.Fatalf("the 5th failure must lock for ~15s, got %v", d)
	}
	_ = f.RecordFailure(t.Context(), "alice", testFree, testBase, testMax)
	if d, _ := f.LockRemaining(t.Context(), "alice"); d < 20*time.Second || d > 2*testBase {
		t.Fatalf("the 6th failure must double the lock (~30s), got %v", d)
	}
	for i := 0; i < 10; i++ {
		_ = f.RecordFailure(t.Context(), "alice", testFree, testBase, testMax)
	}
	if d, _ := f.LockRemaining(t.Context(), "alice"); d > testMax || d < testMax-10*time.Second {
		t.Fatalf("the lock must be capped at %v, got %v", testMax, d)
	}
	if d, _ := f.LockRemaining(t.Context(), "bob"); d != 0 {
		t.Fatalf("other keys are not affected: %v", d)
	}
}

func TestLoginFailuresClear(t *testing.T) {
	f := newTestStore(t, keyString("k1")).LoginFailures()
	for i := 0; i < testFree; i++ {
		_ = f.RecordFailure(t.Context(), "alice", testFree, testBase, testMax)
	}
	if d, _ := f.LockRemaining(t.Context(), "alice"); d == 0 {
		t.Fatal("must be locked")
	}
	if err := f.ClearFailures(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.LockRemaining(t.Context(), "alice"); d != 0 {
		t.Fatalf("a cleared key must be free: %v", d)
	}
}

// Параллельные неудачи на разных репликах не теряются: обновление атомарное.
func TestLoginFailuresConcurrentRecordsAreNotLost(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	f := s.LoginFailures()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.RecordFailure(t.Context(), "alice", 1000, testBase, testMax); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count FROM login_failures WHERE key = $1`, failureKey("alice")).Scan(&n); err != nil || n != 20 {
		t.Fatalf("count=%d err=%v", n, err)
	}
}

// Логин в таблице не хранится открыто.
func TestLoginFailuresDoNotStoreTheLogin(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	_ = s.LoginFailures().RecordFailure(t.Context(), "alice", testFree, testBase, testMax)
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM login_failures WHERE position('alice'::bytea in key) > 0`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the key must be a hash: n=%d err=%v", n, err)
	}
}

func TestLoginFailuresCleanupKeepsActiveLocks(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	f := s.LoginFailures()
	ctx := context.Background()
	for i := 0; i < testFree; i++ {
		_ = f.RecordFailure(t.Context(), "locked", testFree, testBase, testMax)
	}
	_ = f.RecordFailure(t.Context(), "stale", testFree, testBase, testMax)
	_ = f.RecordFailure(t.Context(), "fresh", testFree, testBase, testMax)
	// «stale» и «locked» давно не обновлялись: блокировка действует, значит, «locked» удалять нельзя
	if _, err := s.pool.Exec(ctx, `UPDATE login_failures SET last_at = now() - interval '1 hour'
		WHERE key IN ($1, $2)`, failureKey("stale"), failureKey("locked")); err != nil {
		t.Fatal(err)
	}
	n, err := f.Cleanup(ctx, 15*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("only the stale unlocked record must go: n=%d err=%v", n, err)
	}
	if d, _ := f.LockRemaining(t.Context(), "locked"); d == 0 {
		t.Fatal("an active lock must survive the cleanup")
	}
}

func TestLoginFailuresCleanupCapsTheTable(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO login_failures (key, count, last_at)
		SELECT sha256(i::text::bytea), 1, now() - (i || ' seconds')::interval FROM generate_series(1, $1::int) i`, MaxLoginFailures+50); err != nil {
		t.Fatal(err)
	}
	n, err := s.LoginFailures().Cleanup(ctx, 24*time.Hour)
	if err != nil || n != 50 {
		t.Fatalf("the 50 oldest entries must go: n=%d err=%v", n, err)
	}
}

func TestDeleteAuditBefore(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (ts, username, role, action, result) VALUES
		(now() - interval '100 days', 'u', 'admin', 'Login', 'ok'),
		(now() - interval '40 days',  'u', 'admin', 'Login', 'ok'),
		(now() - interval '1 day',    'u', 'admin', 'Login', 'ok')`); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteAuditBefore(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var left int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&left)
	if left != 1 {
		t.Fatalf("one fresh record must stay, got %d", left)
	}
}

// Задачи обслуживания выполняет одна реплика: вторая не берёт блокировку, пока первая её держит.
func TestJobsKeyHasOneHolder(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	a, b := s.Leader(JobsKey), s.Leader(JobsKey)
	ctx := context.Background()
	if !a.Acquire(ctx) {
		t.Fatal("the first replica must get the lock")
	}
	if b.Acquire(ctx) {
		t.Fatal("the second replica must not get a held lock")
	}
	a.Release()
	if !b.Acquire(ctx) {
		t.Fatal("the lock must pass to another replica after release")
	}
	b.Release()
}

// Отмена контекста вызывающего слоя доходит до PostgreSQL: операция не ждёт собственного таймаута.
func TestLoginFailuresHonorCallerCancellation(t *testing.T) {
	f := newTestStore(t, keyString("k1")).LoginFailures()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.LockRemaining(ctx, "alice"); !errors.Is(err, context.Canceled) {
		t.Fatalf("LockRemaining: want context.Canceled, got %v", err)
	}
	if err := f.RecordFailure(ctx, "alice", testFree, testBase, testMax); !errors.Is(err, context.Canceled) {
		t.Fatalf("RecordFailure: want context.Canceled, got %v", err)
	}
	if err := f.ClearFailures(ctx, "alice"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ClearFailures: want context.Canceled, got %v", err)
	}
}
