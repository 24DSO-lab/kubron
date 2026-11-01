// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type pingTarget struct{}

func (pingTarget) Ping() string { return "pong" }

type obsCall struct {
	method string
	code   int
}

type fakeObserver struct {
	mu      sync.Mutex
	rpc     []obsCall
	statuss []int
}

func (f *fakeObserver) ObserveRPC(method string, code int, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rpc = append(f.rpc, obsCall{method, code})
}

func (f *fakeObserver) ObserveStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuss = append(f.statuss, code)
}

func TestWithRPCObserver(t *testing.T) {
	d, err := NewDispatcher(pingTarget{}, []string{"Ping"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawPlainWriter bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			_, sawPlainWriter = w.(*httptest.ResponseRecorder) // не-RPC: исходный ResponseWriter (Hijacker для WebSocket)
		}
		if r.URL.Path == "/api/rpc/Limited" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok")) // код по умолчанию 200
	})
	obs := &fakeObserver{}
	h := WithRPCObserver(inner, d, obs)

	do := func(method, path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	}
	do(http.MethodPost, "/api/rpc/Ping")
	do(http.MethodPost, "/api/rpc/NoSuchMethod")
	do(http.MethodPost, "/api/rpc/alice@example.com") // пользовательская строка в лейбл не попадает
	do(http.MethodPost, "/api/rpc/Limited")
	do(http.MethodGet, "/api/session")
	do(http.MethodGet, "/api/rpc/Ping") // не POST: не RPC

	want := []obsCall{{"Ping", 200}, {unknownMethod, 200}, {unknownMethod, 200}, {unknownMethod, 429}}
	if len(obs.rpc) != len(want) {
		t.Fatalf("observed %v, want %v", obs.rpc, want)
	}
	for i, w := range want {
		if obs.rpc[i] != w {
			t.Errorf("call %d: %v, want %v", i, obs.rpc[i], w)
		}
	}
	if !sawPlainWriter {
		t.Error("non-RPC requests must get the original ResponseWriter")
	}
	has429 := false
	for _, c := range obs.statuss {
		has429 = has429 || c == http.StatusTooManyRequests
	}
	if !has429 {
		t.Errorf("a 429 must reach ObserveStatus: %v", obs.statuss)
	}
}

func TestWithRPCObserverNilIsTransparent(t *testing.T) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := WithRPCObserver(inner, nil, nil)
	if _, ok := h.(http.HandlerFunc); !ok {
		t.Error("without an observer the handler must be returned as is")
	}
}
