// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Во время сбоя вход не ходит в хранилище на каждой попытке (иначе каждая ждала бы таймаут БД): после первой
// ошибки хранилище не опрашивается sharedRetryAfter, затем одна проба.
func TestStoreOutageDoesNotHammerTheStore(t *testing.T) {
	a, _, db := twoFlakyReplicas(t)
	clk := time.Now()
	a.now = func() time.Time { return clk }

	db.setDown(true)
	db.calls.Store(0)
	for i := 0; i < 8; i++ {
		_, _ = a.NewSession(fmt.Sprintf("10.54.0.%d", i+1)).Login(t.Context(), "admin", "wrong-password")
	}
	if got := db.calls.Load(); got != 1 {
		t.Fatalf("the store was called %d times during the pause, want 1", got)
	}

	clk = clk.Add(sharedRetryAfter + time.Second)
	_, _ = a.NewSession("10.54.1.1").Login(t.Context(), "admin", "wrong-password")
	if got := db.calls.Load(); got != 2 {
		t.Fatalf("after the pause exactly one probe is expected, calls = %d", got)
	}

	// Хранилище вернулось: после паузы и истечения локальной блокировки вход работает в обычном режиме.
	db.setDown(false)
	clk = clk.Add(maxLockout + time.Minute)
	if _, err := a.NewSession("10.54.2.1").Login(t.Context(), "admin", pw); err != nil {
		t.Fatalf("login after the store recovered: %v", err)
	}
}

// guardedBuf — буфер журнала, безопасный для чтения во время записи.
type guardedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (g *guardedBuf) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.b.Write(p)
}

func (g *guardedBuf) lines(substr string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.Count(g.b.String(), substr)
}

// Журнал о недоступности хранилища не растёт по строке на каждую попытку, а метрика считает все.
func TestStoreOutageLogIsRateLimited(t *testing.T) {
	buf := &guardedBuf{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	now := time.Unix(1_700_000_000, 0)
	l := newTestLockouts(&now)
	const marker = "shared login lockout store is unavailable"

	for i := 0; i < 100; i++ {
		l.sharedFailed("lock check", errStoreDown)
	}
	if got := buf.lines(marker); got != 1 {
		t.Fatalf("%d log lines within the interval, want 1", got)
	}
	now = now.Add(sharedLogEvery + time.Second)
	l.sharedFailed("lock check", errStoreDown)
	if got := buf.lines(marker); got != 2 {
		t.Fatalf("%d log lines after the interval, want 2", got)
	}
	if got := l.sharedDegraded.Load(); got != 101 {
		t.Fatalf("sharedDegraded = %d, want 101", got)
	}
}

// Метрика деградации растёт при сбое и стоит на нуле, пока хранилище здорово.
func TestSharedDegradedStats(t *testing.T) {
	a, _, db := twoFlakyReplicas(t)
	failLogins(t, a, "admin", 2, "10.58.0")
	if got := a.Stats().SharedDegraded; got != 0 {
		t.Fatalf("a healthy store must not count as degraded: %d", got)
	}
	db.setDown(true)
	failLogins(t, a, "admin", 1, "10.58.1")
	if got := a.Stats().SharedDegraded; got == 0 {
		t.Fatal("the outage must be visible in Stats")
	}
}

// Без общего хранилища (файл, десктоп) поведение прежнее: счётчик по логину в памяти, метрика деградации нулевая.
func TestNoSharedStoreIsNotDegraded(t *testing.T) {
	a, _, _ := twoReplicas(t) // sharedDB не реализует FailureBackend
	failLogins(t, a, "admin", freeAttempts, "10.59.0")
	mustBeLocked(t, a, "10.59.9.9")
	if got := a.Stats().SharedDegraded; got != 0 {
		t.Fatalf("no shared store is not a degradation: %d", got)
	}
}
