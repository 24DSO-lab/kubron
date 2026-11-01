// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type readyzBody struct {
	Status   string            `json:"status"`
	Draining bool              `json:"draining"`
	Checks   map[string]string `json:"checks"`
}

func readyzGet(t *testing.T, h http.Handler) (int, readyzBody, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var b readyzBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("response is not JSON: %v: %q", err, rec.Body.String())
	}
	return rec.Code, b, rec.Result().Header
}

func TestReadyzWithoutDatabaseIsReady(t *testing.T) {
	code, b, hdr := readyzGet(t, ReadyzHandler(ReadyzConfig{}))
	if code != 200 || b.Status != "ready" || b.Checks["database"] != "skipped" {
		t.Fatalf("no DB (file store) must be ready: %d %+v", code, b)
	}
	if !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") || hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %v", hdr)
	}
}

func TestReadyzChecksDatabase(t *testing.T) {
	var fail atomic.Bool
	h := ReadyzHandler(ReadyzConfig{
		CacheTTL: time.Nanosecond,
		DB: DBPingFunc(func(context.Context) error {
			if fail.Load() {
				return errors.New("dial tcp 10.0.0.5:5432: password=hunter2 refused")
			}
			return nil
		}),
	})
	if code, b, _ := readyzGet(t, h); code != 200 || b.Checks["database"] != "ok" {
		t.Fatalf("healthy DB: %d %+v", code, b)
	}
	fail.Store(true)
	time.Sleep(time.Millisecond)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"not_ready"`) || !strings.Contains(rec.Body.String(), `"fail"`) {
		t.Fatalf("DB down must give 503/not_ready: %d %s", rec.Code, rec.Body)
	}
	// /readyz открыт без входа: текст ошибки БД (адрес, пароль) в ответ не попадает
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatalf("DB error text leaked: %s", rec.Body)
	}
	fail.Store(false)
	time.Sleep(time.Millisecond)
	if code, _, _ := readyzGet(t, h); code != 200 {
		t.Fatalf("the replica must recover when DB returns: %d", code)
	}
}

func TestReadyzDraining(t *testing.T) {
	l := NewLifecycle("r-1")
	h := ReadyzHandler(ReadyzConfig{Drain: l})
	if code, b, _ := readyzGet(t, h); code != 200 || b.Draining {
		t.Fatalf("running replica: %d %+v", code, b)
	}
	l.BeginDrain()
	code, b, _ := readyzGet(t, h)
	if code != 503 || !b.Draining || b.Status != "not_ready" {
		t.Fatalf("draining replica must answer 503: %d %+v", code, b)
	}
}

func TestReadyzDatabaseTimeout(t *testing.T) {
	h := ReadyzHandler(ReadyzConfig{
		Timeout: 20 * time.Millisecond,
		DB:      DBPingFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }),
	})
	start := time.Now()
	code, b, _ := readyzGet(t, h)
	if code != 503 || b.Checks["database"] != "fail" {
		t.Fatalf("a hanging DB must fail the check: %d %+v", code, b)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the check must be bounded by Timeout, took %v", time.Since(start))
	}
}

func TestReadyzCachesDatabaseCheck(t *testing.T) {
	var calls atomic.Int32
	h := ReadyzHandler(ReadyzConfig{CacheTTL: time.Minute, DB: DBPingFunc(func(context.Context) error { calls.Add(1); return nil })})
	for i := 0; i < 5; i++ {
		readyzGet(t, h)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("probes inside the cache window must not hit the DB: %d pings", n)
	}
}

func TestReadyzAbortedRequestDoesNotPoisonCache(t *testing.T) {
	h := ReadyzHandler(ReadyzConfig{CacheTTL: time.Minute, DB: DBPingFunc(func(ctx context.Context) error { return ctx.Err() })})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	if rec.Code != 200 {
		t.Fatalf("a cancelled client request must not mark the DB as down: %d %s", rec.Code, rec.Body)
	}
}

func TestReadyzLogsOnlyTransitions(t *testing.T) {
	var lines []string
	l := NewLifecycle("r-1")
	h := ReadyzHandler(ReadyzConfig{Drain: l, Logf: func(f string, a ...any) { lines = append(lines, f) }})
	for i := 0; i < 5; i++ {
		readyzGet(t, h)
	}
	l.BeginDrain()
	for i := 0; i < 5; i++ {
		readyzGet(t, h)
	}
	if len(lines) != 2 {
		t.Fatalf("expected two log lines (ready, not ready), got %d: %v", len(lines), lines)
	}
}

func TestNewHandlerServesReadyz(t *testing.T) {
	l := NewLifecycle("r-1")
	g := &Guard{Lifecycle: l}
	h := NewHandler(hardeningDispatcher(t), nil, g, WithReadyz(ReadyzConfig{}))
	if code, _, _ := readyzGet(t, h); code != 200 {
		t.Fatalf("readyz via NewHandler: %d", code)
	}
	l.BeginDrain() // Drain подхватывается из Guard.Lifecycle, если не задан явно
	if code, _, _ := readyzGet(t, h); code != 503 {
		t.Fatalf("draining must be taken from Guard.Lifecycle: %d", code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/readyz", nil))
	if rec.Code == 200 {
		t.Errorf("POST /readyz must not succeed: %d", rec.Code)
	}
}
