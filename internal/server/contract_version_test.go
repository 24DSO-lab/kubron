// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func contractServer(build string) http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return contractGuard(build, ok)
}

func TestContractGuard(t *testing.T) {
	cases := []struct {
		name, server, header, query string
		want                        int
	}{
		{"same build", "1.2.0", "1.2.0", "", http.StatusNoContent},
		{"other build", "1.2.0", "1.1.0", "", http.StatusConflict},
		{"other build in the query (WebSocket)", "1.2.0", "", "1.1.0", http.StatusConflict},
		{"same build in the query", "1.2.0", "", "1.2.0", http.StatusNoContent},
		{"client without a build (curl, probes)", "1.2.0", "", "", http.StatusNoContent},
		{"dev client", "1.2.0", "dev", "", http.StatusNoContent},
		{"dev server disables the check", "dev", "1.1.0", "", http.StatusNoContent},
		{"server without a build disables the check", "", "1.1.0", "", http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := "/api/rpc/X"
			if c.query != "" {
				url += "?build=" + c.query
			}
			req := httptest.NewRequest(http.MethodPost, url, nil)
			if c.header != "" {
				req.Header.Set(BuildHeader, c.header)
			}
			rec := httptest.NewRecorder()
			contractServer(c.server).ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status %d, expected %d", rec.Code, c.want)
			}
			if c.want == http.StatusConflict {
				var body rpcResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == nil || body.Error.Code != "contract_mismatch" {
					t.Errorf("expected the contract_mismatch error, got %s", rec.Body.String())
				}
				if got := rec.Header().Get(BuildHeader); got != c.server {
					t.Errorf("the response must name the server build, got %q", got)
				}
			}
		})
	}
}

// Проверка подключена ко всем трём маршрутам браузера и не задевает остальные.
func TestNewHandlerAppliesContractGuard(t *testing.T) {
	d, _ := newTestDispatcher(t)
	h := NewHandler(d, nil, nil, WithContractBuild("2.0.0"))

	req := httptest.NewRequest(http.MethodPost, "/api/rpc/Add", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(BuildHeader, "1.0.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("RPC of another build: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz must not be checked: status %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rpc/Add", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(BuildHeader, "2.0.0")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusConflict {
		t.Errorf("the same build must pass the contract check")
	}
}
