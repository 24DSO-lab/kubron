// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLifecycleNilSafe(t *testing.T) {
	var l *Lifecycle
	l.BeginDrain()
	l.markClosing()
	if l.Draining() || l.Closing() || l.ReplicaID() != "" {
		t.Error("a nil Lifecycle must never be draining")
	}
	var _ Drainer = l
}

func TestLifecycleStates(t *testing.T) {
	l := NewLifecycle("r-1")
	if l.ReplicaID() != "r-1" || l.Draining() || l.Closing() {
		t.Fatal("initial state")
	}
	l.BeginDrain()
	l.BeginDrain() // идемпотентно
	if !l.Draining() || l.Closing() {
		t.Fatal("after BeginDrain: draining, but connections are not closed yet")
	}
	l.markClosing()
	if !l.Draining() || !l.Closing() {
		t.Fatal("after markClosing")
	}
}

// readClose читает кадры до закрытия и возвращает код закрытия, присланный сервером.
func readClose(t *testing.T, ws *websocket.Conn) int {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			if ce, ok := err.(*websocket.CloseError); ok {
				return ce.Code
			}
			t.Fatalf("expected a close frame, got: %v", err)
		}
	}
}

func haSetup(t *testing.T) (srv *httptest.Server, g *Guard, cookie string) {
	t.Helper()
	hub := NewHub()
	ss := NewSessions(time.Hour, true)
	ss.OnEnd = hub.CloseSession
	d, err := NewDispatcher(&guardTarget{}, []string{"Login"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g = &Guard{Sessions: ss, Hub: hub, Lifecycle: NewLifecycle("r-1")}
	srv = httptest.NewServer(NewHandler(d, nil, g))
	t.Cleanup(srv.Close)
	tok, _ := ss.Create("u1", nil)
	return srv, g, cookieName + "=" + tok
}

func TestHubCloseAllUsesCodeAndRejectsLateConnections(t *testing.T) {
	h := NewHub()
	c := h.Register("s1")
	h.CloseAll(CloseServiceRestart, "bye")
	select {
	case <-c.Done():
	default:
		t.Fatal("the connection must be closed")
	}
	if code, reason := c.closeInfo(); code != CloseServiceRestart || reason != "bye" {
		t.Errorf("close frame: %d %q", code, reason)
	}
	late := h.Register("s2")
	select {
	case <-late.Done():
	default:
		t.Fatal("a connection registered after CloseAll must be closed at once")
	}
	if code, _ := late.closeInfo(); code != CloseServiceRestart {
		t.Errorf("late connection code: %d", code)
	}
	if h.Len() != 0 {
		t.Errorf("hub must be empty: %d", h.Len())
	}
}

func TestConnCloseKeepsFirstCode(t *testing.T) {
	h := NewHub()
	c := h.Register("s1")
	c.CloseWith(CloseServiceRestart, "restart")
	c.Close()
	if code, _ := c.closeInfo(); code != CloseServiceRestart {
		t.Errorf("the first close code must stay: %d", code)
	}
	n := h.Register("s2")
	n.Close()
	if code, reason := n.closeInfo(); code != closeNormal || reason != "" {
		t.Errorf("a plain Close is a normal closure: %d %q", code, reason)
	}
}

func TestWSClosedWithServiceRestartOnShutdown(t *testing.T) {
	srv, g, cookie := haSetup(t)
	ws, _, err := dial(srv, http.Header{"Cookie": {cookie}, "Origin": {srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	readJSON(t, ws) // ready

	g.BeginDrain()
	g.CloseConnections()
	if code := readClose(t, ws); code != CloseServiceRestart {
		t.Errorf("close code %d, want %d", code, CloseServiceRestart)
	}
}

func TestWSRefusedWhileDraining(t *testing.T) {
	srv, g, cookie := haSetup(t)
	g.BeginDrain()
	_, resp, err := dial(srv, http.Header{"Cookie": {cookie}, "Origin": {srv.URL}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a draining replica must answer 503: err=%v resp=%v", err, resp)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("Retry-After is expected")
	}
}

func TestSessionStaysValidWhileDraining(t *testing.T) {
	_, g, _ := haSetup(t)
	tok, _ := g.Sessions.Create("u2", nil)
	g.BeginDrain()
	g.CloseConnections()
	if _, ok := g.Sessions.Get(tok); !ok {
		t.Error("shutting a replica down must not end web sessions")
	}
}

type fakeTermConn struct {
	mu      sync.Mutex
	closed  bool
	control [][]byte
}

func (f *fakeTermConn) Close() error { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }
func (f *fakeTermConn) WriteControl(_ int, data []byte, _ time.Time) error {
	f.mu.Lock()
	f.control = append(f.control, data)
	f.mu.Unlock()
	return nil
}

func TestTermConnsShutdownSendsCloseFrame(t *testing.T) {
	var tc termConns
	a, b := &fakeTermConn{}, &fakeTermConn{}
	tc.add("s1", a)
	tc.add("s2", b)
	tc.shutdown(CloseServiceRestart, closeReasonRestart)
	for _, f := range []*fakeTermConn{a, b} {
		if !f.closed || len(f.control) != 1 {
			t.Fatalf("socket must get a close frame and be closed: %+v", f)
		}
		if want := websocket.FormatCloseMessage(CloseServiceRestart, closeReasonRestart); string(f.control[0]) != string(want) {
			t.Errorf("close frame %x, want %x", f.control[0], want)
		}
	}
}

func TestServeWithOrderAndTimeouts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var steps []string
	add := func(s string) { mu.Lock(); steps = append(steps, s); mu.Unlock() }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	var drainAt, closeAt time.Time
	go func() {
		done <- ServeWith(ctx, ln, http.NotFoundHandler(), ShutdownPlan{
			Drain:      func() { drainAt = time.Now(); add("drain") },
			DrainDelay: 150 * time.Millisecond,
			CloseConns: func() { closeAt = time.Now(); add("close") },
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeWith: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(steps, ",") != "drain,close" {
		t.Fatalf("order: %v", steps)
	}
	if closeAt.Sub(drainAt) < 140*time.Millisecond {
		t.Errorf("the drain delay was not honoured: %s", closeAt.Sub(drainAt))
	}
	if time.Since(start) > 4*time.Second {
		t.Error("the shutdown took too long")
	}
}
