// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/24DSO-labs/kubron/internal/termbuf"
)

type termFixture struct {
	srv    *httptest.Server
	reg    *termbuf.Registry
	ss     *Sessions
	cookie string
	buf    *termbuf.Buffer
	id     string
	mu     sync.Mutex
	input  []byte
	cols   uint16
	rows   uint16
}

func newTermFixture(t *testing.T) *termFixture {
	t.Helper()
	f := &termFixture{reg: termbuf.NewRegistry(), ss: NewSessions(time.Hour, true), id: termbuf.NewID()}
	g := &Guard{Sessions: f.ss, Terminals: f.reg}
	f.ss.OnEnd = g.CloseSession
	tok, sess := f.ss.Create("u1", nil)
	f.cookie = cookieName + "=" + tok
	buf, err := f.reg.Register(f.id, termbuf.Hooks{
		Owner:  sess.ID,
		Input:  func(p []byte) error { f.mu.Lock(); f.input = append(f.input, p...); f.mu.Unlock(); return nil },
		Resize: func(c, r uint16) error { f.mu.Lock(); f.cols, f.rows = c, r; f.mu.Unlock(); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.buf = buf
	d, _ := NewDispatcher(&guardTarget{}, []string{"Login"}, nil)
	f.srv = httptest.NewServer(NewHandler(d, nil, g))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *termFixture) dial(id string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	if hdr == nil {
		hdr = http.Header{"Cookie": {f.cookie}, "Origin": {f.srv.URL}}
	}
	return websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.srv.URL, "http")+"/api/terminal/"+id, hdr)
}

func TestTerminalWSRejections(t *testing.T) {
	f := newTermFixture(t)
	if _, resp, err := f.dial(f.id, http.Header{"Origin": {f.srv.URL}}); err == nil || resp.StatusCode != 401 {
		t.Errorf("no cookie: %v %v", err, resp)
	}
	if _, resp, err := f.dial("0000", nil); err == nil || resp.StatusCode != 404 {
		t.Errorf("unknown id: %v %v", err, resp)
	}
	if _, resp, err := f.dial(f.id, http.Header{"Cookie": {f.cookie}}); err == nil || resp.StatusCode != 403 {
		t.Errorf("no Origin: %v %v", err, resp)
	}
	if _, resp, err := f.dial(f.id, http.Header{"Cookie": {f.cookie}, "Origin": {"https://evil.example"}}); err == nil || resp.StatusCode != 403 {
		t.Errorf("foreign Origin: %v %v", err, resp)
	}
}

func TestTerminalWSOutputInputResizeAndExit(t *testing.T) {
	f := newTermFixture(t)
	f.buf.Data([]byte("prompt$ ")) // вывод до подключения не теряется
	ws, _, err := f.dial(f.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	typ, msg, err := ws.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage || string(msg) != "prompt$ " {
		t.Fatalf("first frame: %v %q %v", typ, msg, err)
	}

	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		ok := string(f.input) == "ls\n" && f.cols == 100 && f.rows == 30
		f.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("input/resize did not arrive: %q %dx%d", f.input, f.cols, f.rows)
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.buf.Data([]byte("out"))
	if _, msg, err := ws.ReadMessage(); err != nil || string(msg) != "out" {
		t.Fatalf("output: %q %v", msg, err)
	}
	f.buf.Exit()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if !ok || ce.Code != websocket.CloseNormalClosure || ce.Text != termCloseExit {
		t.Errorf("expected close 1000 'exit': %v", err)
	}
}

func TestTerminalWSSecondConnectionRejected(t *testing.T) {
	f := newTermFixture(t)
	ws1, _, err := f.dial(f.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws1.Close()
	time.Sleep(100 * time.Millisecond)
	ws2, _, err := f.dial(f.id, nil)
	if err != nil {
		t.Fatal(err) // рукопожатие проходит, затем сервер закрывает с политикой
	}
	defer ws2.Close()
	_ = ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws2.ReadMessage()
	if ce, ok := err.(*websocket.CloseError); !ok || ce.Code != websocket.ClosePolicyViolation {
		t.Errorf("expected close 1008: %v", err)
	}
}

func TestTerminalWSClosedWhenSessionEnds(t *testing.T) {
	f := newTermFixture(t)
	ws, _, err := f.dial(f.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	time.Sleep(100 * time.Millisecond)
	f.ss.Create("u2", nil) // вытесняет сессию с терминалом
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Error("the socket must close")
	}
}

func TestTerminalWSForeignSessionGetsNotFound(t *testing.T) {
	f := newTermFixture(t)
	f.ss.Exclusive = false // две сессии одновременно: владелец терминала и посторонний
	other, _ := f.ss.Create("u2", nil)
	hdr := http.Header{"Cookie": {cookieName + "=" + other}, "Origin": {f.srv.URL}}
	if _, resp, err := f.dial(f.id, hdr); err == nil || resp.StatusCode != 404 {
		t.Errorf("a foreign terminal must be indistinguishable from a missing one: %v %v", err, resp)
	}
}
