// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func wsSetup(t *testing.T) (srv *httptest.Server, hub *Hub, ss *Sessions, cookie string) {
	t.Helper()
	hub = NewHub()
	ss = NewSessions(time.Hour, true)
	ss.OnEnd = hub.CloseSession
	tg := &guardTarget{}
	d, err := NewDispatcher(tg, []string{"Login"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv = httptest.NewServer(NewHandler(d, nil, &Guard{Sessions: ss, Hub: hub}))
	t.Cleanup(srv.Close)
	tok, _ := ss.Create("u1", nil)
	return srv, hub, ss, cookieName + "=" + tok
}

func dial(srv *httptest.Server, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", hdr)
}

func readJSON(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m map[string]any
	if err := ws.ReadJSON(&m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func TestWSRejectsWithoutSessionOrOrigin(t *testing.T) {
	srv, _, _, cookie := wsSetup(t)
	if _, resp, err := dial(srv, http.Header{"Origin": {srv.URL}}); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Errorf("no cookie: err=%v resp=%v", err, resp)
	}
	if _, resp, err := dial(srv, http.Header{"Cookie": {cookie}}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Errorf("no Origin: err=%v resp=%v", err, resp)
	}
	if _, resp, err := dial(srv, http.Header{"Cookie": {cookie}, "Origin": {"https://evil.example"}}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Errorf("foreign Origin: err=%v resp=%v", err, resp)
	}
}

func TestWSSubscribeAndReceive(t *testing.T) {
	srv, hub, _, cookie := wsSetup(t)
	ws, _, err := dial(srv, http.Header{"Cookie": {cookie}, "Origin": {srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if m := readJSON(t, ws); m["op"] != "ready" {
		t.Fatalf("ready: %v", m)
	}
	_ = ws.WriteJSON(map[string]any{"op": "sub", "events": []string{"cluster:changed"}})
	if m := readJSON(t, ws); m["op"] != "sub_ok" {
		t.Fatalf("sub_ok: %v", m)
	}
	hub.Emit("cluster:changed", "prod")
	m := readJSON(t, ws)
	b, _ := json.Marshal(m)
	if m["event"] != "cluster:changed" || !strings.Contains(string(b), "prod") {
		t.Errorf("event: %s", b)
	}
}

func TestWSClosedWhenSessionEnds(t *testing.T) {
	srv, _, ss, cookie := wsSetup(t)
	ws, _, err := dial(srv, http.Header{"Cookie": {cookie}, "Origin": {srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	readJSON(t, ws)      // ready
	ss.Create("u2", nil) // вытесняет первую сессию
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return // соединение закрыто сервером
		}
	}
}

// Кадр ready называет реплику сокета: браузер выдаёт потокам идентификаторы с этим префиксом (ADR-0016).
func TestWSReadyNamesTheReplica(t *testing.T) {
	hub := NewHub()
	ss := NewSessions(time.Hour, true)
	d, err := NewDispatcher(&guardTarget{}, []string{"Login"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(d, nil, &Guard{Sessions: ss, Hub: hub, Lifecycle: NewLifecycle("kubron-0_ab12")}))
	defer srv.Close()
	tok, _ := ss.Create("u1", nil)
	ws, _, err := dial(srv, http.Header{"Cookie": {cookieName + "=" + tok}, "Origin": {srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if m := readJSON(t, ws); m["op"] != "ready" || m["replica"] != "kubron-0_ab12" {
		t.Fatalf("ready: %v", m)
	}
}
