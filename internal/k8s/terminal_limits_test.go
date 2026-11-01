// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type sinkRecorder struct {
	mu   sync.Mutex
	data strings.Builder
}

func (s *sinkRecorder) Data(p []byte) { s.mu.Lock(); s.data.Write(p); s.mu.Unlock() }
func (s *sinkRecorder) Exit()         {}
func (s *sinkRecorder) text() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.data.String() }

// newWatchedSession создаёт сессию без кластера: достаточно полей, которые трогают сторож и CloseTerminal.
func newWatchedSession(m *Manager, id string, out TerminalOutput, ended chan<- string) *TerminalSession {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	s := &TerminalSession{ID: id, Started: time.Now(), out: out, stdinW: w, stdoutR: r, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	s.lastInput.Store(time.Now().UnixNano())
	s.onEnd = func(reason string) { ended <- reason }
	m.terminalSessions.Add(s)
	return s
}

func waitReason(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("the session was not closed in time")
		return ""
	}
}

func TestTerminalClosedAfterMaxDuration(t *testing.T) {
	m := &Manager{terminalSessions: NewTerminalSessionManager(), limits: Limits{TerminalMaxDuration: 80 * time.Millisecond}}
	ended, out := make(chan string, 2), &sinkRecorder{}
	s := newWatchedSession(m, "t1", out, ended)
	m.watchTerminal(s)
	if r := waitReason(t, ended); r != TerminalEndMaxDuration {
		t.Errorf("reason = %q", r)
	}
	if !strings.Contains(out.text(), "maximum session duration") {
		t.Errorf("the user must be told why: %q", out.text())
	}
	if m.terminalSessions.Get("t1") != nil {
		t.Error("the session must be removed")
	}
}

func TestTerminalIdleTimeoutCountsInputOnly(t *testing.T) {
	m := &Manager{terminalSessions: NewTerminalSessionManager(), limits: Limits{TerminalIdleTimeout: 200 * time.Millisecond}}
	ended := make(chan string, 2)
	s := newWatchedSession(m, "t1", &sinkRecorder{}, ended)
	m.watchTerminal(s)
	for i := 0; i < 4; i++ { // ввод каждые 100 мс удерживает сессию дольше срока простоя
		time.Sleep(100 * time.Millisecond)
		s.lastInput.Store(time.Now().UnixNano())
	}
	select {
	case r := <-ended:
		t.Fatalf("an active session was closed: %q", r)
	default:
	}
	if r := waitReason(t, ended); r != TerminalEndIdle {
		t.Errorf("reason = %q", r)
	}
}

func TestTerminalEndReportedOnce(t *testing.T) {
	m := &Manager{terminalSessions: NewTerminalSessionManager()}
	ended := make(chan string, 4)
	s := newWatchedSession(m, "t1", &sinkRecorder{}, ended)
	if err := m.CloseTerminal("t1"); err != nil {
		t.Fatal(err)
	}
	s.finish(TerminalEndExit) // поток завершается после закрытия: второй отчёт не нужен
	if r := waitReason(t, ended); r != TerminalEndClosed {
		t.Errorf("the first reason wins: %q", r)
	}
	select {
	case r := <-ended:
		t.Errorf("the end was reported twice: %q", r)
	default:
	}
}

func TestIsNodeShellPod(t *testing.T) {
	for _, c := range []struct {
		ns, pod string
		want    bool
	}{
		{"kube-system", "kubron-node-shell-x7k2p", true},
		{"default", "kubron-node-shell-x7k2p", false},
		{"kube-system", "coredns-abc", false},
	} {
		if got := IsNodeShellPod(c.ns, c.pod); got != c.want {
			t.Errorf("%s/%s = %v", c.ns, c.pod, got)
		}
	}
}
