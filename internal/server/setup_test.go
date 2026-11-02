// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
)

const testSetupToken = "setup-token-0123456789"

// setupTarget имитирует ядро: первый SetupAdmin создаёт администратора, остальные получают ErrSetupDone.
type setupTarget struct {
	mu    sync.Mutex
	done  bool
	calls atomic.Int32
}

func (s *setupTarget) SetupAdmin(name, pass string) (*auth.UserInfo, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil, auth.ErrSetupDone
	}
	s.done = true
	return &auth.UserInfo{ID: "id-" + name, Username: name}, nil
}
func (s *setupTarget) AuthStatus() (*auth.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &auth.Status{NeedsSetup: !s.done}, nil
}

func newSetupHandler(t *testing.T, gate *SetupGate) (http.Handler, *setupTarget) {
	t.Helper()
	tg := &setupTarget{}
	d, err := NewDispatcher(tg, []string{"SetupAdmin", "AuthStatus"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &Guard{Sessions: NewSessions(time.Hour, true), Setup: gate}
	return NewHandler(d, nil, g), tg
}

func pendingGate(token string) *SetupGate {
	return &SetupGate{Token: token, Limiter: NewRateLimiter(1.0/30, 5)}
}

func decodeReply(rec *httptest.ResponseRecorder) rpcReply {
	var r rpcReply
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return r
}

func postSetup(h http.Handler, token, remote string) (*httptest.ResponseRecorder, rpcReply) {
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/SetupAdmin", strings.NewReader(`["admin","correct horse"]`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(SetupTokenHeader, token)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, decodeReply(rec)
}

func TestSetupRequiresTokenInServiceMode(t *testing.T) {
	h, tg := newSetupHandler(t, pendingGate(testSetupToken))
	for name, tok := range map[string]string{"missing": "", "wrong": "wrong-token-0123456789", "prefix": testSetupToken[:10]} {
		rec, rep := postSetup(h, tok, "")
		if rec.Code != http.StatusForbidden || rep.Error == nil || rep.Error.Code != "setup_token_invalid" {
			t.Errorf("%s token: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if tg.calls.Load() != 0 {
		t.Fatal("SetupAdmin must not be reached without a valid token")
	}
	if rec, rep := postSetup(h, testSetupToken, ""); rec.Code != 200 || rep.Error != nil || cookieOf(rec) == nil {
		t.Fatalf("valid token: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupDisabledWithoutConfiguredToken(t *testing.T) {
	h, tg := newSetupHandler(t, pendingGate(""))
	for _, tok := range []string{"", "anything-at-all-0123"} {
		rec, rep := postSetup(h, tok, "")
		if rec.Code != http.StatusForbidden || rep.Error == nil || rep.Error.Code != "setup_disabled" {
			t.Fatalf("token %q: %d %s", tok, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rep.Error.Message, "create-admin") || !strings.Contains(rep.Error.Message, "KUBRON_SETUP_TOKEN") {
			t.Errorf("the message must say how to proceed: %q", rep.Error.Message)
		}
	}
	if tg.calls.Load() != 0 {
		t.Fatal("setup must not run")
	}
}

func TestSetupWithoutGateNeedsNoToken(t *testing.T) { // loopback и десктоп
	h, _ := newSetupHandler(t, nil)
	if rec, rep := postSetup(h, "", ""); rec.Code != 200 || rep.Error != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupAfterCompletionReportsDoneEvenWithValidToken(t *testing.T) {
	h, _ := newSetupHandler(t, &SetupGate{Token: testSetupToken})
	if rec, _ := postSetup(h, testSetupToken, ""); rec.Code != 200 {
		t.Fatalf("first setup: %d", rec.Code)
	}
	rec, rep := postSetup(h, testSetupToken, "")
	if rep.Error == nil || !strings.Contains(rep.Error.Message, auth.ErrSetupDone.Error()) {
		t.Fatalf("second setup: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupPendingFalseSkipsTokenCheck(t *testing.T) {
	tg := &setupTarget{done: true}
	d, _ := NewDispatcher(tg, []string{"SetupAdmin", "AuthStatus"}, nil)
	gate := &SetupGate{Token: testSetupToken, Pending: func() bool { return false }}
	h := NewHandler(d, nil, &Guard{Sessions: NewSessions(time.Hour, true), Setup: gate})
	rec, rep := postSetup(h, "", "")
	if rep.Error == nil || !strings.Contains(rep.Error.Message, auth.ErrSetupDone.Error()) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupWrongTokensAreRateLimitedPerAddress(t *testing.T) {
	h, tg := newSetupHandler(t, pendingGate(testSetupToken))
	for i := 0; i < 5; i++ {
		if rec, _ := postSetup(h, "wrong-token-0123456789", "198.51.100.7:1000"); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	// После серии неудач даже верный токен с этого адреса не принимается
	rec, rep := postSetup(h, testSetupToken, "198.51.100.7:1001")
	if rec.Code != http.StatusTooManyRequests || rep.Error == nil || rep.Error.Code != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked address: %d %s", rec.Code, rec.Body.String())
	}
	if tg.calls.Load() != 0 {
		t.Fatal("setup must not run for a blocked address")
	}
	// Другой адрес не затронут
	if rec, _ := postSetup(h, testSetupToken, "203.0.113.9:2000"); rec.Code != 200 {
		t.Fatalf("other address: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupValidAttemptsDoNotSpendTheBudget(t *testing.T) {
	gate := pendingGate(testSetupToken)
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/rpc/SetupAdmin", nil)
		req.Header.Set(SetupTokenHeader, testSetupToken)
		g := &Guard{Setup: gate}
		if !g.checkSetup(httptest.NewRecorder(), req) {
			t.Fatalf("valid attempt %d was rejected", i)
		}
	}
}

func TestConcurrentSetupAdminCreatesOneAdmin(t *testing.T) {
	h, tg := newSetupHandler(t, pendingGate(testSetupToken))
	const n = 12
	var ok atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if rec, rep := postSetup(h, testSetupToken, ""); rec.Code == 200 && rep.Error == nil {
				ok.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 || tg.calls.Load() != n {
		t.Fatalf("created=%d calls=%d, want 1 and %d", ok.Load(), tg.calls.Load(), n)
	}
}

func TestAuthStatusAnnouncesTokenWithoutLeakingIt(t *testing.T) {
	h, _ := newSetupHandler(t, pendingGate(testSetupToken))
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/AuthStatus", strings.NewReader(`[]`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `"setupTokenRequired":true`) || !strings.Contains(body, `"needsSetup":true`) {
		t.Fatalf("status must announce the token: %s", body)
	}
	if strings.Contains(body, testSetupToken) {
		t.Fatal("the setup token must never appear in API responses")
	}
	// Без защиты (loopback) подсказки нет
	h2, _ := newSetupHandler(t, nil)
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, req.Clone(req.Context()))
	if strings.Contains(rec.Body.String(), "setupTokenRequired") {
		t.Fatalf("no token hint without the gate: %s", rec.Body.String())
	}
}

func TestTokenEqual(t *testing.T) {
	if !tokenEqual("a-secret", "a-secret") || tokenEqual("a-secret", "a-secreT") || tokenEqual("", "a-secret") || tokenEqual("a-secret-longer", "a-secret") {
		t.Fatal("tokenEqual compares exact values only")
	}
}

func TestConfigSetupTokenFromEnvAndLength(t *testing.T) {
	tokenEnv := func(v string) func(string) string {
		return func(k string) string {
			if k == "KUBRON_SETUP_TOKEN" {
				return v
			}
			return ""
		}
	}
	cfg, err := LoadConfig(nil, tokenEnv("  "+testSetupToken+"\n"), io.Discard)
	if err != nil || cfg.SetupToken != testSetupToken {
		t.Fatalf("token: %q %v", cfg.SetupToken, err)
	}
	if _, err := LoadConfig(nil, tokenEnv("short"), io.Discard); err == nil {
		t.Fatal("a short token must be rejected")
	}
	if cfg, err := LoadConfig(nil, tokenEnv(""), io.Discard); err != nil || cfg.SetupToken != "" {
		t.Fatalf("empty token: %q %v", cfg.SetupToken, err)
	}
}
