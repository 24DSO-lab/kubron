// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/logging"
)

type leakyTarget struct{}

// Leak возвращает ошибку, в тексте которой есть секреты разных видов: так выглядит ошибка драйвера БД,
// клиента кластера или OIDC-провайдера.
func (leakyTarget) Leak() error {
	return errors.New("connect postgres://kubron:Sup3rDbPass@db:5432/kubron failed; Authorization: Bearer abc.def-ghi123; password=hunter2hunter2")
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logging.ResetSecrets()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(logging.New(logging.Options{Writer: &buf, Level: slog.LevelDebug}))
	t.Cleanup(func() { slog.SetDefault(old); logging.ResetSecrets() })
	return &buf
}

func TestWithRequestIDGeneratesAndPropagates(t *testing.T) {
	var seen string
	h := WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = logging.RequestID(r.Context())
		_, ok := w.(*httptest.ResponseRecorder) // ResponseWriter не подменяется: WebSocket и Flush работают
		if !ok {
			t.Error("the ResponseWriter must not be wrapped")
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	got := rec.Header().Get("X-Request-Id")
	if len(got) != 16 || got != seen {
		t.Fatalf("generated id: header %q, context %q", got, seen)
	}
}

func TestWithRequestIDAcceptsOnlySafeIncomingIDs(t *testing.T) {
	h := WithRequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	do := func(id string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-Id", id)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("X-Request-Id")
	}
	if got := do("proxy-req-12345678"); got != "proxy-req-12345678" {
		t.Errorf("a valid id from the proxy must be kept: %q", got)
	}
	for _, bad := range []string{"x", strings.Repeat("a", 200), `a","level":"ERROR","msg":"forged`, "id with spaces 123"} {
		if got := do(bad); got == bad || len(got) != 16 {
			t.Errorf("%q must be replaced by a generated id, got %q", bad, got)
		}
	}
}

// Ошибка RPC попадает в журнал с request_id и без секретов; отладочные детали остаются читаемыми.
func TestRPCErrorLogCarriesRequestIDAndNoSecrets(t *testing.T) {
	buf := captureLogs(t)
	d, err := NewDispatcher(leakyTarget{}, []string{"Leak"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := WithRequestID(NewHandler(d, nil, nil))
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/Leak", strings.NewReader("[]"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-0123456789")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}

	out := buf.String()
	for _, secret := range []string{"Sup3rDbPass", "abc.def-ghi123", "hunter2hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q leaked into the log: %s", secret, out)
		}
	}
	var rec1 map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		m := map[string]any{}
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "rpc call failed" {
			rec1 = m
		}
	}
	if rec1 == nil {
		t.Fatalf("the failed call is not in the log: %s", out)
	}
	if rec1["request_id"] != "req-0123456789" || rec1["method"] != "Leak" || rec1["level"] != "ERROR" {
		t.Errorf("record: %v", rec1)
	}
	if _, ok := rec1["error"]; ok {
		t.Errorf("the raw error must not be logged: %v", rec1)
	}
	if _, ok := rec1["error_type"].(string); !ok {
		t.Errorf("the error type must remain available for diagnostics: %v", rec1)
	}
}
