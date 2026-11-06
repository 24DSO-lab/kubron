// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"testing"
	"time"
)

func newTestLockouts(now *time.Time) *lockouts {
	l := &lockouts{}
	l.init(func() time.Time { return *now }, nil)
	return l
}

func TestLockoutsStartAfterFreeAttemptsAndExpire(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newTestLockouts(&now)

	for i := 0; i < freeAttempts-1; i++ {
		l.recordFailureLocked("alice", freeAttempts)
	}
	if d := l.lockRemainingLocked("alice"); d != 0 {
		t.Fatalf("locked before the limit: %v", d)
	}
	l.recordFailureLocked("alice", freeAttempts)
	if d := l.lockRemainingLocked("alice"); d != baseLockout {
		t.Fatalf("first lockout = %v, want %v", d, baseLockout)
	}
	if got := l.lockoutsStarted.Load(); got != 1 {
		t.Fatalf("lockoutsStarted = %d", got)
	}
	if d := l.lockRemainingLocked("bob"); d != 0 {
		t.Fatalf("a lockout must not leak to another key: %v", d)
	}

	now = now.Add(baseLockout + time.Second)
	if d := l.lockRemainingLocked("alice"); d != 0 {
		t.Fatalf("lockout must expire, still %v", d)
	}
}

func TestLockoutsGrowAndAreCapped(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newTestLockouts(&now)
	for i := 0; i < freeAttempts+20; i++ {
		l.recordFailureLocked("alice", freeAttempts)
	}
	if d := l.lockRemainingLocked("alice"); d != maxLockout {
		t.Fatalf("lockout = %v, want the cap %v", d, maxLockout)
	}
}

func TestLockoutsFollowTheServiceClock(t *testing.T) {
	// часы читаются при каждом вызове: подмена shared.now действует и на блокировки
	now := time.Unix(1_700_000_000, 0)
	sh := &shared{now: func() time.Time { return now }}
	sh.lockouts.init(func() time.Time { return sh.now() }, nil)

	for i := 0; i < freeAttempts; i++ {
		sh.recordFailureLocked("alice", freeAttempts)
	}
	sh.now = func() time.Time { return now.Add(time.Hour) }
	if d := sh.lockRemainingLocked("alice"); d != 0 {
		t.Fatalf("the replaced clock was ignored: %v", d)
	}
}
