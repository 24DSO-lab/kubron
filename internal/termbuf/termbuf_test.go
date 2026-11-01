// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package termbuf

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSink struct {
	mu     sync.Mutex
	got    bytes.Buffer
	exited bool
	err    error
}

func (f *fakeSink) Send(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.got.Write(p)
	return nil
}
func (f *fakeSink) Exit() { f.mu.Lock(); f.exited = true; f.mu.Unlock() }
func (f *fakeSink) text() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got.String()
}
func (f *fakeSink) isExited() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.exited }

func TestNewIDIsRandomAndLong(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 32 || a == b {
		t.Errorf("ID: %q %q", a, b)
	}
}

func TestOutputBeforeAttachIsReplayed(t *testing.T) {
	r := NewRegistry()
	b, _ := r.Register("t1", Hooks{})
	b.Data([]byte("user@pod:~$ "))
	s := &fakeSink{}
	if _, err := r.Attach("t1", s); err != nil {
		t.Fatal(err)
	}
	b.Data([]byte("ls\r\n"))
	if s.text() != "user@pod:~$ ls\r\n" {
		t.Errorf("got %q", s.text())
	}
}

func TestBufferKeepsOnlyLastBytes(t *testing.T) {
	var b Buffer
	chunk := bytes.Repeat([]byte("a"), MaxBuffered)
	b.Data(chunk)
	b.Data([]byte("TAIL"))
	if len(b.pending) != MaxBuffered || !bytes.HasSuffix(b.pending, []byte("TAIL")) {
		t.Errorf("buffer: %d bytes, tail %q", len(b.pending), b.pending[len(b.pending)-4:])
	}
}

func TestBufferCopiesCallerSlice(t *testing.T) {
	var b Buffer
	p := []byte("abc")
	b.Data(p)
	copy(p, "xyz") // читатель вывода переиспользует свой буфер
	if string(b.pending) != "abc" {
		t.Errorf("buffer is corrupted: %q", b.pending)
	}
}

func TestExitBeforeAndAfterAttach(t *testing.T) {
	r := NewRegistry()
	b, _ := r.Register("t", Hooks{})
	b.Data([]byte("bye"))
	b.Exit()
	s := &fakeSink{}
	a, err := r.Attach("t", s)
	if err != nil || s.text() != "bye" || !s.isExited() {
		t.Fatalf("after exit: err=%v text=%q exited=%v", err, s.text(), s.isExited())
	}
	a.Detach()
	if r.Exists("t") {
		t.Error("a finished session must be removed on detach")
	}

	b2, _ := r.Register("t2", Hooks{})
	s2 := &fakeSink{}
	_, _ = r.Attach("t2", s2)
	b2.Exit()
	if !s2.isExited() {
		t.Error("Exit did not reach the attached client")
	}
}

func TestSecondAttachRejectedUntilDetach(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Register("t", Hooks{})
	a, err := r.Attach("t", &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Attach("t", &fakeSink{}); !errors.Is(err, ErrAttached) {
		t.Errorf("expected ErrAttached: %v", err)
	}
	a.Detach()
	if _, err := r.Attach("t", &fakeSink{}); err != nil {
		t.Errorf("reattach after detach: %v", err)
	}
	if _, err := r.Attach("missing", &fakeSink{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound: %v", err)
	}
}

func TestBrokenSinkFallsBackToBuffering(t *testing.T) {
	r := NewRegistry()
	b, _ := r.Register("t", Hooks{})
	bad := &fakeSink{}
	_, _ = r.Attach("t", bad)
	bad.err = errors.New("socket closed")
	b.Data([]byte("lost?"))
	good := &fakeSink{}
	// Первое подключение ещё числится занятым (клиент не вызвал Detach): освобождаем как это делает обработчик
	r.items["t"].attached = false
	if _, err := r.Attach("t", good); err != nil || good.text() != "lost?" {
		t.Errorf("err=%v text=%q", err, good.text())
	}
}

func TestInputAndResizeHooks(t *testing.T) {
	r := NewRegistry()
	var in []byte
	var cols, rows uint16
	_, _ = r.Register("t", Hooks{
		Input:  func(p []byte) error { in = append(in, p...); return nil },
		Resize: func(c, ro uint16) error { cols, rows = c, ro; return nil },
	})
	a, _ := r.Attach("t", &fakeSink{})
	_ = a.Input([]byte("ls"))
	_ = a.Resize(120, 40)
	if string(in) != "ls" || cols != 120 || rows != 40 {
		t.Errorf("in=%q size=%dx%d", in, cols, rows)
	}
}

func TestSweepClosesNeverAttachedAndLongDetached(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.now = func() time.Time { return now }
	closed := map[string]int{}
	reg := func(id string) {
		_, _ = r.Register(id, Hooks{Close: func() { closed[id]++ }})
	}
	reg("never")
	reg("detached")
	reg("live")
	a, _ := r.Attach("detached", &fakeSink{})
	a.Detach()
	_, _ = r.Attach("live", &fakeSink{})

	now = now.Add(20 * time.Second)
	if n := r.Sweep(); n != 0 {
		t.Fatalf("too early: %d closed", n)
	}
	now = now.Add(20 * time.Second) // 40 с: дедлайны 30 с прошли
	if n := r.Sweep(); n != 2 {
		t.Fatalf("%d closed, expected 2", n)
	}
	if closed["never"] != 1 || closed["detached"] != 1 || closed["live"] != 0 {
		t.Errorf("closes: %v", closed)
	}
	if r.Len() != 1 || !r.Exists("live") {
		t.Errorf("only the attached session must remain: %d", r.Len())
	}
}

func TestSweepDropsOldExitedWithoutClosing(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.now = func() time.Time { return now }
	called := false
	b, _ := r.Register("t", Hooks{Close: func() { called = true }})
	b.Exit()
	now = now.Add(time.Minute)
	if r.Sweep() != 0 || r.Exists("t") || called {
		t.Errorf("a finished session must simply disappear: exists=%v closeCalled=%v", r.Exists("t"), called)
	}
}

func TestRegisterLimit(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < MaxSessions; i++ {
		if _, err := r.Register(NewID(), Hooks{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Register(NewID(), Hooks{}); !errors.Is(err, ErrTooMany) {
		t.Errorf("expected ErrTooMany: %v", err)
	}
}

func TestCloseAll(t *testing.T) {
	r := NewRegistry()
	n := 0
	for i := 0; i < 3; i++ {
		_, _ = r.Register(NewID(), Hooks{Close: func() { n++ }})
	}
	r.CloseAll()
	if n != 3 || r.Len() != 0 {
		t.Errorf("%d closed, %d left", n, r.Len())
	}
}

func TestConcurrentDataAndAttach(t *testing.T) {
	r := NewRegistry()
	b, _ := r.Register("t", Hooks{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			b.Data([]byte("x"))
		}
	}()
	s := &fakeSink{}
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		a, _ := r.Attach("t", s)
		if a != nil {
			a.Detach()
		}
	}()
	wg.Wait()
}

func TestOwnedBy(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Register("t", Hooks{Owner: "s1"})
	if !r.OwnedBy("t", "s1") || r.OwnedBy("t", "s2") || r.OwnedBy("nope", "s1") {
		t.Error("a terminal must be accessible only to its owner")
	}
}

func TestAttachAsChecksOwnerOnAttachAndEveryFrame(t *testing.T) {
	r := NewRegistry()
	var in []byte
	_, _ = r.Register("t", Hooks{Owner: "alice", Input: func(p []byte) error { in = append(in, p...); return nil },
		Resize: func(c, ro uint16) error { return nil }})
	if _, err := r.AttachAs("t", "mallory", &fakeSink{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a foreign owner must look like a missing terminal, got %v", err)
	}
	if _, err := r.AttachAs("t", "", &fakeSink{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an empty owner must be refused, got %v", err)
	}
	a, err := r.AttachAs("t", "alice", &fakeSink{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Input([]byte("ls")); err != nil || string(in) != "ls" {
		t.Fatalf("owner input: %v %q", err, in)
	}
	r.Remove("t") // сессию закрыли или заменили после подключения
	if err := a.Input([]byte("rm")); !errors.Is(err, ErrNotFound) || string(in) != "ls" {
		t.Errorf("input after the session is gone must be refused: %v %q", err, in)
	}
	if err := a.Resize(10, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("resize after the session is gone must be refused: %v", err)
	}
}
