// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// lockSet — одна «блокировка БД» на несколько реплик: берёт первый, держит, пока не отпустит.
type lockSet struct{ holder atomic.Pointer[string] }

type replicaLock struct {
	set  *lockSet
	name string
	rel  atomic.Int32
}

func (l *replicaLock) Acquire(context.Context) bool {
	if cur := l.set.holder.Load(); cur != nil {
		return *cur == l.name
	}
	return l.set.holder.CompareAndSwap(nil, &l.name)
}

func (l *replicaLock) Release() {
	l.rel.Add(1)
	if cur := l.set.holder.Load(); cur != nil && *cur == l.name {
		l.set.holder.Store(nil)
	}
}

func TestOnlyTheLockHolderRuns(t *testing.T) {
	set := &lockSet{}
	var runsA, runsB atomic.Int32
	mk := func(name string, runs *atomic.Int32) *Job {
		return &Job{Name: "cleanup", Lock: &replicaLock{set: set, name: name},
			Run: func(context.Context) (int, error) { runs.Add(1); return 1, nil }}
	}
	a, b := mk("a", &runsA), mk("b", &runsB)
	for i := 0; i < 3; i++ {
		a.Tick(context.Background())
		b.Tick(context.Background())
	}
	if runsA.Load() != 3 || runsB.Load() != 0 {
		t.Fatalf("only the first replica must run: a=%d b=%d", runsA.Load(), runsB.Load())
	}
}

// Исполнитель упал и отпустил блокировку: следующая реплика подхватывает работу на ближайшем такте.
func TestAnotherReplicaTakesOverAfterRelease(t *testing.T) {
	set := &lockSet{}
	la, lb := &replicaLock{set: set, name: "a"}, &replicaLock{set: set, name: "b"}
	var runsB atomic.Int32
	a := &Job{Name: "x", Lock: la, Run: func(context.Context) (int, error) { return 0, nil }}
	b := &Job{Name: "x", Lock: lb, Run: func(context.Context) (int, error) { runsB.Add(1); return 0, nil }}
	a.Tick(context.Background())
	b.Tick(context.Background())
	if runsB.Load() != 0 {
		t.Fatal("b must wait while a holds the lock")
	}
	la.Release()
	b.Tick(context.Background())
	if runsB.Load() != 1 {
		t.Fatal("b must take over after a released the lock")
	}
}

func TestErrorsAndPanicsAreLoggedNotFatal(t *testing.T) {
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, f) }
	(&Job{Name: "e", Logf: logf, Run: func(context.Context) (int, error) { return 0, errors.New("db down") }}).Tick(context.Background())
	(&Job{Name: "p", Logf: logf, Run: func(context.Context) (int, error) { panic("boom") }}).Tick(context.Background())
	if len(logged) != 2 {
		t.Fatalf("both failures must be logged: %v", logged)
	}
}

func TestRunTicksAndReleasesOnStop(t *testing.T) {
	l := &replicaLock{set: &lockSet{}, name: "a"}
	var runs atomic.Int32
	j := &Job{Name: "r", Lock: l, Every: 10 * time.Millisecond, Run: func(context.Context) (int, error) { runs.Add(1); return 0, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, j); close(done) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if runs.Load() < 2 {
		t.Fatalf("the job must run periodically, got %d", runs.Load())
	}
	if l.rel.Load() != 1 {
		t.Fatalf("the lock must be released once, got %d", l.rel.Load())
	}
}

func TestNoLockRunsEveryTick(t *testing.T) {
	var runs atomic.Int32
	j := &Job{Name: "n", Run: func(context.Context) (int, error) { runs.Add(1); return 0, nil }}
	j.Tick(context.Background())
	j.Tick(context.Background())
	if runs.Load() != 2 {
		t.Fatalf("got %d", runs.Load())
	}
}
