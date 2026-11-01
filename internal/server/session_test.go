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

	"github.com/24DSO-labs/kubron/internal/auth"
)

// guardTarget имитирует ядро с одной общей сессией: Login запоминает пользователя.
type guardTarget struct{ user *auth.UserInfo }

func (g *guardTarget) Login(name, pass string) (*auth.UserInfo, error) {
	if pass != "ok" {
		return nil, auth.ErrInvalidCredentials
	}
	g.user = &auth.UserInfo{ID: "id-" + name, Username: name}
	return g.user, nil
}
func (g *guardTarget) Logout() { g.user = nil }
func (g *guardTarget) AuthStatus() (*auth.Status, error) {
	return &auth.Status{User: g.user}, nil
}
func (g *guardTarget) Add(a, b int) (int, error) {
	if g.user == nil {
		return 0, auth.ErrNotAuthenticated
	}
	return a + b, nil
}

func newGuarded(t *testing.T) (http.Handler, *guardTarget, *Sessions) {
	t.Helper()
	tg := &guardTarget{}
	d, err := NewDispatcher(tg, []string{"Login", "Logout", "AuthStatus", "Add"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ss := NewSessions(time.Hour, true)
	return NewHandler(d, nil, &Guard{Sessions: ss}), tg, ss
}

func cookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName || c.Name == secureCookieName {
			return c
		}
	}
	return nil
}

func call(h http.Handler, method, body string, c *http.Cookie, csrf string) (*httptest.ResponseRecorder, rpcReply) {
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/"+method, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c != nil {
		req.AddCookie(c)
	}
	if csrf != "" {
		req.Header.Set(csrfHeader, csrf)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var rep rpcReply
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	return rec, rep
}

func login(t *testing.T, h http.Handler, name string) (*http.Cookie, string) {
	t.Helper()
	rec, rep := call(h, "Login", `["`+name+`","ok"]`, nil, "")
	c := cookieOf(rec)
	if rec.Code != 200 || rep.Error != nil || c == nil {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	return c, rec.Header().Get(csrfHeader)
}

func TestLoginSetsHardenedCookieAndCSRF(t *testing.T) {
	h, _, _ := newGuarded(t)
	c, csrf := login(t, h, "ann")
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure || c.MaxAge <= 0 {
		t.Errorf("cookie: %+v", c)
	}
	if len(c.Value) < 40 || csrf == "" || csrf == c.Value {
		t.Errorf("tokens: cookie=%q csrf=%q", c.Value, csrf)
	}
}

func TestSecureCookieBehindTrustedProxyAndTLS(t *testing.T) {
	d, _ := NewDispatcher(&guardTarget{}, []string{"Login"}, nil)
	h := NewHandler(d, nil, &Guard{Sessions: NewSessions(time.Hour, true), TrustProxy: true})
	rec, _ := post(h, "/api/rpc/Login", `["a","ok"]`, map[string]string{"X-Forwarded-Proto": "https"})
	c := cookieOf(rec)
	if c == nil || !c.Secure || c.Name != secureCookieName {
		t.Errorf("expected a __Host- cookie with Secure: %+v", c)
	}
	// Без доверия к прокси заголовок игнорируется.
	h = NewHandler(d, nil, &Guard{Sessions: NewSessions(time.Hour, true)})
	rec, _ = post(h, "/api/rpc/Login", `["a","ok"]`, map[string]string{"X-Forwarded-Proto": "https"})
	if c := cookieOf(rec); c == nil || c.Secure {
		t.Errorf("X-Forwarded-Proto without trust-proxy must not enable Secure: %+v", c)
	}
}

func TestProtectedMethodNeedsSessionAndCSRF(t *testing.T) {
	h, _, _ := newGuarded(t)
	if rec, rep := call(h, "Add", `[1,2]`, nil, ""); rec.Code != 401 || rep.Error.Code != "unauthenticated" {
		t.Errorf("no session: %d %s", rec.Code, rec.Body.String())
	}
	c, csrf := login(t, h, "ann")
	if rec, rep := call(h, "Add", `[1,2]`, c, ""); rec.Code != 403 || rep.Error.Code != "csrf" {
		t.Errorf("no CSRF: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := call(h, "Add", `[1,2]`, c, "foreign"); rec.Code != 403 {
		t.Errorf("with a foreign CSRF: %d", rec.Code)
	}
	if rec, rep := call(h, "Add", `[1,2]`, c, csrf); rec.Code != 200 || string(rep.Result) != "3" {
		t.Errorf("with session and CSRF: %d %s", rec.Code, rec.Body.String())
	}
	forged := &http.Cookie{Name: cookieName, Value: "forged-token"}
	if rec, _ := call(h, "Add", `[1,2]`, forged, csrf); rec.Code != 401 {
		t.Errorf("foreign cookie: %d", rec.Code)
	}
}

func TestAuthStatusHidesUserWithoutSession(t *testing.T) {
	h, _, _ := newGuarded(t)
	c, csrf := login(t, h, "ann")
	_, anon := call(h, "AuthStatus", `[]`, nil, "")
	if strings.Contains(string(anon.Result), "ann") {
		t.Errorf("anonymous sees a user: %s", anon.Result)
	}
	_, mine := call(h, "AuthStatus", `[]`, c, csrf)
	if !strings.Contains(string(mine.Result), "ann") {
		t.Errorf("the session owner does not see themselves: %s", mine.Result)
	}
}

func TestExclusiveLoginRevokesPreviousSession(t *testing.T) {
	h, _, ss := newGuarded(t)
	c1, csrf1 := login(t, h, "ann")
	c2, csrf2 := login(t, h, "bob")
	if ss.Len() != 1 {
		t.Fatalf("%d sessions, expected 1", ss.Len())
	}
	if rec, _ := call(h, "Add", `[1,1]`, c1, csrf1); rec.Code != 401 {
		t.Errorf("the old session must be revoked: %d", rec.Code)
	}
	if rec, _ := call(h, "Add", `[1,1]`, c2, csrf2); rec.Code != 200 {
		t.Errorf("new session: %d", rec.Code)
	}
}

func TestFailedLoginKeepsSessionsAndSetsNoCookie(t *testing.T) {
	h, _, ss := newGuarded(t)
	c, csrf := login(t, h, "ann")
	rec, _ := call(h, "Login", `["bob","плохой"]`, nil, "")
	if rec.Code == 200 || cookieOf(rec) != nil {
		t.Errorf("failed login: %d cookie=%v", rec.Code, cookieOf(rec))
	}
	if rec, _ := call(h, "Add", `[1,1]`, c, csrf); rec.Code != 200 || ss.Len() != 1 {
		t.Errorf("ann's session must stay alive: %d", rec.Code)
	}
}

func TestLogoutClosesSession(t *testing.T) {
	h, _, ss := newGuarded(t)
	c, csrf := login(t, h, "ann")
	rec, _ := call(h, "Logout", `[]`, c, csrf)
	if rec.Code != 200 || ss.Len() != 0 {
		t.Fatalf("logout: %d, %d sessions", rec.Code, ss.Len())
	}
	if cc := cookieOf(rec); cc == nil || cc.MaxAge >= 0 {
		t.Errorf("the cookie must be deleted: %+v", cc)
	}
	if rec, _ := call(h, "Add", `[1,1]`, c, csrf); rec.Code != 401 {
		t.Errorf("after logout: %d", rec.Code)
	}
}

func TestCoreSessionLossDropsWebSession(t *testing.T) {
	h, tg, ss := newGuarded(t)
	c, csrf := login(t, h, "ann")
	tg.user = nil // ядро закрыло сессию по простою
	rec, _ := call(h, "Add", `[1,1]`, c, csrf)
	if rec.Code != 401 || ss.Len() != 0 {
		t.Errorf("expected 401 and session removal: %d, %d sessions", rec.Code, ss.Len())
	}
}

func TestSessionExpiresAbsolutely(t *testing.T) {
	ss := NewSessions(time.Minute, false)
	now := time.Now()
	ss.now = func() time.Time { return now }
	tok, _ := ss.Create("u", nil)
	if _, ok := ss.Get(tok); !ok {
		t.Fatal("the session must be alive")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := ss.Get(tok); ok {
		t.Error("the session must expire")
	}
}

func TestSessionsNonExclusiveKeepsMany(t *testing.T) {
	ss := NewSessions(time.Hour, false)
	ss.Create("a", nil)
	ss.Create("b", nil)
	if ss.Len() != 2 {
		t.Errorf("expected 2, have %d", ss.Len())
	}
}

func TestSessionEndpoint(t *testing.T) {
	h, _, _ := newGuarded(t)
	get := func(c *http.Cookie) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	if m := get(nil); m["authenticated"] != false || m["csrfToken"] != nil {
		t.Errorf("anonymous: %v", m)
	}
	c, csrf := login(t, h, "ann")
	if m := get(c); m["authenticated"] != true || m["csrfToken"] != csrf {
		t.Errorf("logged in: %v", m)
	}
}

func TestCrossSiteFetchMetadataRejected(t *testing.T) {
	h, _, _ := newGuarded(t)
	for _, v := range []string{"cross-site", "same-site"} {
		if rec, _ := post(h, "/api/rpc/AuthStatus", `[]`, map[string]string{"Sec-Fetch-Site": v}); rec.Code != 403 {
			t.Errorf("Sec-Fetch-Site=%s: %d", v, rec.Code)
		}
	}
	if rec, _ := post(h, "/api/rpc/AuthStatus", `[]`, map[string]string{"Sec-Fetch-Site": "same-origin"}); rec.Code != 200 {
		t.Errorf("same-origin: %d", rec.Code)
	}
}

func TestReapEndsSessionsWithDeadViewsAndReleasesThem(t *testing.T) {
	ss := NewSessions(time.Hour, false)
	var released []any
	var ended []string
	ss.Release = func(v any) { released = append(released, v) }
	ss.OnEnd = func(id string) { ended = append(ended, id) }

	live, dead := &struct{ n int }{1}, &struct{ n int }{2}
	tokLive, sLive := ss.Create("a", live)
	_, sDead := ss.Create("b", dead)
	_, _ = ss.Create("c", nil) // без вида (общий экземпляр) уборка не трогает

	n := ss.Reap(func(v any) bool { return v == live })
	if n != 1 || len(released) != 1 || released[0] != dead || len(ended) != 1 || ended[0] != sDead.ID {
		t.Fatalf("Reap=%d released=%v ended=%v", n, released, ended)
	}
	if _, ok := ss.Get(tokLive); !ok {
		t.Error("the live session must remain")
	}
	if ss.Len() != 2 {
		t.Errorf("%d sessions left, expected 2 (live and view-less)", ss.Len())
	}
	_ = sLive
}

func TestViewsListsLiveViewsOnly(t *testing.T) {
	ss := NewSessions(time.Hour, false)
	a, b := &struct{ n int }{1}, &struct{ n int }{2}
	_, _ = ss.Create("a", a)
	tokB, _ := ss.Create("b", b)
	_, _ = ss.Create("c", nil) // без вида
	if v := ss.Views(); len(v) != 2 {
		t.Fatalf("got %d views, expected 2", len(v))
	}
	ss.Delete(tokB)
	if v := ss.Views(); len(v) != 1 || v[0] != any(a) {
		t.Fatalf("got %v", v)
	}
}

func TestReapAlsoSweepsExpired(t *testing.T) {
	ss := NewSessions(time.Hour, false)
	now := time.Now()
	ss.now = func() time.Time { return now }
	var released int
	ss.Release = func(any) { released++ }
	ss.Create("a", &struct{}{})
	now = now.Add(2 * time.Hour)
	if n := ss.Reap(func(any) bool { return true }); n != 1 || released != 1 {
		t.Fatalf("Reap=%d released=%d", n, released)
	}
}
