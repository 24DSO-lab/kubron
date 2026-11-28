// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/sso"
)

type fakeSSOProvider struct {
	flow     sso.FlowState
	identity sso.Identity
	err      error
	used     bool
}

func (p *fakeSSOProvider) Begin(redirectURL string) (string, sso.FlowState, error) {
	p.flow.RedirectURL = redirectURL
	return "https://idp.example.test/authorize?state=" + p.flow.State, p.flow, nil
}

func (p *fakeSSOProvider) Complete(_ context.Context, flow sso.FlowState, query url.Values) (sso.Identity, error) {
	if p.err != nil {
		return sso.Identity{}, p.err
	}
	if p.used {
		return sso.Identity{}, sso.ErrAuthorization
	}
	if query.Get("state") != flow.State || query.Get("code") != "authorization-code" {
		return sso.Identity{}, sso.ErrStateMismatch
	}
	p.used = true
	return p.identity, nil
}

func newFakeSSOProvider() *fakeSSOProvider {
	return &fakeSSOProvider{
		flow: sso.FlowState{
			State: "test-state", Nonce: "test-nonce", Verifier: "test-verifier",
			RedirectURL: "https://kubron.example.test/auth/sso/callback", CreatedAt: time.Now().UTC(),
		},
		identity: sso.Identity{Issuer: "https://idp.example.test", Subject: "subject-1", Username: "alice"},
	}
}

func TestSSOLoginCallbackCreatesWebSession(t *testing.T) {
	provider := newFakeSSOProvider()
	sessions := NewSessions(time.Hour, false)
	sessions.Release = func(any) {}
	bound := false
	g := &Guard{
		Sessions: sessions,
		NewView: func(string) any { return &struct{}{} },
		Bind: func(any, string) { bound = true },
	}
	login := func(_ context.Context, _ any, id sso.Identity) (*auth.UserInfo, error) {
		if id.Subject != "subject-1" {
			t.Fatalf("identity subject = %q", id.Subject)
		}
		return &auth.UserInfo{ID: "user-1"}, nil
	}
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodGet, "/auth/sso/login?return=https://attacker.example", nil)
	ssoLoginHandler(g, &ssoHandlerConfig{
		provider: provider, flowKey: []byte("test-flow-key"), publicURL: "https://kubron.example.test", login: login,
	}).ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusFound || loginRec.Header().Get("Location") != "https://idp.example.test/authorize?state=test-state" {
		t.Fatalf("login response = %d, location %q", loginRec.Code, loginRec.Header().Get("Location"))
	}
	var flowCookie *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		if cookie.Name == ssoFlowCookie {
			flowCookie = cookie
		}
	}
	if flowCookie == nil || !flowCookie.HttpOnly || flowCookie.SameSite != http.SameSiteLaxMode || flowCookie.Path != "/" || flowCookie.MaxAge != 600 {
		t.Fatalf("flow cookie = %+v", flowCookie)
	}

	callbackRec := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/sso/callback?state=test-state&code=authorization-code", nil)
	callbackReq.AddCookie(flowCookie)
	ssoCallbackHandler(g, &ssoHandlerConfig{
		provider: provider, flowKey: []byte("test-flow-key"), login: login,
	}).ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusFound || callbackRec.Header().Get("Location") != "/" {
		t.Fatalf("callback response = %d, location %q, body %s", callbackRec.Code, callbackRec.Header().Get("Location"), callbackRec.Body.String())
	}
	if !bound {
		t.Fatal("successful SSO session was not bound")
	}
	var sessionCookie bool
	var clearedFlow bool
	for _, cookie := range callbackRec.Result().Cookies() {
		if cookie.Name == cookieName && cookie.Value != "" {
			sessionCookie = true
		}
		if cookie.Name == ssoFlowCookie && cookie.MaxAge < 0 {
			clearedFlow = true
		}
	}
	if !sessionCookie || !clearedFlow {
		t.Fatalf("session cookie=%v cleared flow cookie=%v", sessionCookie, clearedFlow)
	}
}

func TestSSOCallbackRejectsDuplicateStateAndClearsCookie(t *testing.T) {
	provider := newFakeSSOProvider()
	g := &Guard{Sessions: NewSessions(time.Hour, false)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/sso/callback?state=one&state=two&code=authorization-code", nil)
	req.AddCookie(&http.Cookie{Name: ssoFlowCookie, Value: "invalid"})
	ssoCallbackHandler(g, &ssoHandlerConfig{provider: provider, flowKey: []byte("key")}).ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?sso_error=denied" {
		t.Fatalf("response = %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != ssoFlowCookie || cookies[0].MaxAge >= 0 {
		t.Fatalf("flow cookie was not cleared: %+v", cookies)
	}
}

func TestSSOCallbackRejectsMissingAndInvalidFlowCookies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		code   string
	}{
		{name: "missing", code: "expired"},
		{name: "invalid", cookie: &http.Cookie{Name: ssoFlowCookie, Value: "not-a-sealed-flow"}, code: "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &Guard{Sessions: NewSessions(time.Hour, false)}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/auth/sso/callback?state=test-state&code=authorization-code", nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			ssoCallbackHandler(g, &ssoHandlerConfig{provider: newFakeSSOProvider(), flowKey: []byte("key")}).ServeHTTP(rec, req)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?sso_error="+tc.code {
				t.Fatalf("response = %d, location %q", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestSSOLoginSetsHostCookieOnSecureRequests(t *testing.T) {
	g := &Guard{Sessions: NewSessions(time.Hour, false)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://kubron.example.test/auth/sso/login", nil)
	ssoLoginHandler(g, &ssoHandlerConfig{
		provider: newFakeSSOProvider(), flowKey: []byte("key"), publicURL: "https://kubron.example.test",
	}).ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != secureSSOFlowCookie || !cookies[0].Secure {
		t.Fatalf("secure flow cookie = %+v", cookies)
	}
}

func TestSSOCallbackMapsDisabledAndProvisioningErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{sso.ErrFlowExpired, "expired"},
		{sso.ErrAuthorization, "denied"},
		{auth.ErrAccountDisabled, "disabled"},
		{auth.ErrExternalNotProvisioned, "not_provisioned"},
		{errors.New("database unavailable"), "unavailable"},
	} {
		if got := ssoFailureCode(tc.err); got != tc.code {
			t.Errorf("ssoFailureCode(%v) = %q, want %q", tc.err, got, tc.code)
		}
	}
}

func TestSSOLoginUsesAnonymousRateLimit(t *testing.T) {
	provider := newFakeSSOProvider()
	g := &Guard{Sessions: NewSessions(time.Hour, false), AnonLimiter: NewRateLimiter(1, 1)}
	h := ssoLoginHandler(g, &ssoHandlerConfig{
		provider: provider, flowKey: []byte("key"), publicURL: "https://kubron.example.test",
	})
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/auth/sso/login", nil))
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/auth/sso/login", nil))
	if first.Code != http.StatusFound || second.Code != http.StatusTooManyRequests {
		t.Fatalf("responses = %d, %d", first.Code, second.Code)
	}
}

func TestLocalLoginDisabledBeforeDispatch(t *testing.T) {
	target := &localLoginTarget{}
	d, err := NewDispatcher(target, []string{"Login"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &Guard{Sessions: NewSessions(time.Hour, false), DisableLocalLogin: true}
	h := NewHandler(d, nil, g)
	rec, reply := post(h, "/api/rpc/Login", "[]", nil)
	if rec.Code != http.StatusForbidden || reply.Error == nil || reply.Error.Code != "local_login_disabled" {
		t.Fatalf("response = %d, %+v", rec.Code, reply.Error)
	}
	if target.calls != 0 {
		t.Fatal("Login was dispatched while local login is disabled")
	}
}

type localLoginTarget struct{ calls int }

func (t *localLoginTarget) Login() error {
	t.calls++
	return nil
}

func TestSSOStatusIsAddedToAuthStatus(t *testing.T) {
	target := &ssoStatusTarget{}
	d, err := NewDispatcher(target, []string{"AuthStatus"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &Guard{
		Sessions: NewSessions(time.Hour, false),
		SSOStatus: &auth.SSOStatus{Enabled: true, Label: "Company SSO", LoginURL: "/auth/sso/login", LocalLoginDisabled: true},
	}
	h := NewHandler(d, nil, g)
	rec, reply := post(h, "/api/rpc/AuthStatus", "[]", nil)
	if rec.Code != http.StatusOK || !strings.Contains(string(reply.Result), "\"sso\"") ||
		!strings.Contains(string(reply.Result), "\"localLoginDisabled\":true") {
		t.Fatalf("AuthStatus response = %d, %s", rec.Code, reply.Result)
	}
}

type ssoStatusTarget struct {
	user *auth.UserInfo
}

func (t *ssoStatusTarget) AuthStatus() (*auth.Status, error) {
	return &auth.Status{NeedsSetup: false, User: t.user}, nil
}

func TestSSOHTTPFlowReturnsAuthenticatedStatus(t *testing.T) {
	provider := newFakeSSOProvider()
	target := &ssoStatusTarget{}
	d, err := NewDispatcher(target, []string{"AuthStatus"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(time.Hour, false)
	sessions.Release = func(any) {}
	g := &Guard{
		Sessions: sessions,
		NewView: func(string) any { return &ssoStatusTarget{} },
	}
	login := func(_ context.Context, view any, _ sso.Identity) (*auth.UserInfo, error) {
		user := &auth.UserInfo{ID: "user-1", Username: "alice"}
		view.(*ssoStatusTarget).user = user
		return user, nil
	}
	h := NewHandler(d, nil, g, WithSSO(provider, []byte("flow-key"), "https://kubron.example.test", login, true, "Company SSO"))

	start := httptest.NewRecorder()
	h.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/auth/sso/login", nil))
	var flowCookie *http.Cookie
	for _, cookie := range start.Result().Cookies() {
		if cookie.Name == ssoFlowCookie {
			flowCookie = cookie
		}
	}
	if flowCookie == nil {
		t.Fatal("login did not set the flow cookie")
	}

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/sso/callback?state=test-state&code=authorization-code", nil)
	callbackReq.AddCookie(flowCookie)
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d, %q", callback.Code, callback.Header().Get("Location"))
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == cookieName && cookie.Value != "" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("callback did not create a Kubron session cookie")
	}

	statusRec, statusReply := post(h, "/api/rpc/AuthStatus", "[]", map[string]string{
		"Cookie": cookieName + "=" + sessionCookie.Value,
	})
	if statusRec.Code != http.StatusOK || !strings.Contains(string(statusReply.Result), "\"username\":\"alice\"") ||
		!strings.Contains(string(statusReply.Result), "\"loginUrl\":\"/auth/sso/login\"") {
		t.Fatalf("AuthStatus = %d, %s", statusRec.Code, statusReply.Result)
	}

	replay := httptest.NewRecorder()
	replayReq := httptest.NewRequest(http.MethodGet, "/auth/sso/callback?state=test-state&code=authorization-code", nil)
	replayReq.AddCookie(flowCookie)
	h.ServeHTTP(replay, replayReq)
	if replay.Code != http.StatusFound || replay.Header().Get("Location") != "/?sso_error=denied" {
		t.Fatalf("callback replay = %d, %q", replay.Code, replay.Header().Get("Location"))
	}
}
