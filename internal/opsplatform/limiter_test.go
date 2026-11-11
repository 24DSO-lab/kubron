// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterNeverExceedsLimit(t *testing.T) {
	t.Parallel()
	const limit, workers = 3, 40
	l := NewClusterLimiter(limit)
	var cur, peak atomic.Int32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(t.Context(), "c1")
			if err != nil {
				t.Error(err)
				return
			}
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			cur.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if got := peak.Load(); got > limit {
		t.Fatalf("peak concurrency %d exceeds limit %d", got, limit)
	}
	if l.tracked() != 0 {
		t.Fatalf("limiter leaked %d cluster entries", l.tracked())
	}
}

func TestLimiterClustersAreIndependent(t *testing.T) {
	t.Parallel()
	l := NewClusterLimiter(1)
	relA, err := l.Acquire(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer relA()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	relB, err := l.Acquire(ctx, "b")
	if err != nil {
		t.Fatalf("a busy cluster a must not block cluster b: %v", err)
	}
	relB()
}

func TestLimiterCancelWhileWaiting(t *testing.T) {
	t.Parallel()
	l := NewClusterLimiter(1)
	hold, err := l.Acquire(t.Context(), "c")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		_, err := l.Acquire(ctx, "c")
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	err = <-errc
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire after cancel = %v, want ErrUnavailable and context.Canceled", err)
	}
	hold()
	if l.tracked() != 0 {
		t.Fatalf("slot or entry leaked after cancel: %d", l.tracked())
	}
	// слот снова доступен
	rel, err := l.Acquire(t.Context(), "c")
	if err != nil {
		t.Fatalf("slot not reusable: %v", err)
	}
	rel()
}

func TestLimiterAlreadyCanceled(t *testing.T) {
	t.Parallel()
	l := NewClusterLimiter(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := l.Acquire(ctx, "c"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if l.tracked() != 0 {
		t.Fatal("entry leaked")
	}
}

func TestLimiterReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	l := NewClusterLimiter(1)
	rel, err := l.Acquire(t.Context(), "c")
	if err != nil {
		t.Fatal(err)
	}
	rel()
	rel() // второй вызов не должен освободить чужой слот
	r1, err := l.Acquire(t.Context(), "c")
	if err != nil {
		t.Fatal(err)
	}
	defer r1()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "c"); err == nil {
		t.Fatal("double release freed a slot that belongs to another holder")
	}
}

func TestConcurrencyFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, val string
		want      int
	}{
		{"empty", "", DefaultClusterConcurrency},
		{"garbage", "many", DefaultClusterConcurrency},
		{"zero", "0", DefaultClusterConcurrency},
		{"negative", "-3", DefaultClusterConcurrency},
		{"ok", "7", 7},
		{"capped", "1000", MaxClusterConcurrency},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ConcurrencyFromEnv(func(k string) string {
				if k != EnvClusterConcurrency {
					t.Errorf("unexpected variable %q", k)
				}
				return tc.val
			})
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNewClusterLimiterBounds(t *testing.T) {
	t.Parallel()
	if got := NewClusterLimiter(0).Limit(); got != DefaultClusterConcurrency {
		t.Fatalf("0 -> %d", got)
	}
	if got := NewClusterLimiter(999).Limit(); got != MaxClusterConcurrency {
		t.Fatalf("999 -> %d", got)
	}
}
