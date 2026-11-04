// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// browser plays the user's browser for the desktop loopback flow: it follows the provider's redirect to the
// local listener.
func browser(t *testing.T, idp *fakeIdP, user string) func(string) error {
	t.Helper()
	return func(authURL string) error {
		resp, err := http.Get(idp.authorize(authURL, user))
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
}

func loopbackEnv(t *testing.T, idp *fakeIdP, open func(string) error) credential.Env {
	env := idp.env(true)
	env.OpenURL = open
	return env
}

func loopbackDescriptor(idp *fakeIdP, extra map[string]string) credential.Descriptor {
	args := map[string]string{"listen-address": "127.0.0.1:0"}
	for k, v := range extra {
		args[k] = v
	}
	return idp.descriptor("authcode", args)
}

func poll(p *Provider, env credential.Env, d credential.Descriptor, res credential.Result) (credential.Result, error) {
	return p.Continue(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: *res.Challenge, Pending: res.Pending})
}

func TestLoopbackAuthcode(t *testing.T) {
	idp := newFakeIdP(t, false)
	var opened string
	br := browser(t, idp, "alice")
	env := loopbackEnv(t, idp, func(u string) error { opened = u; return nil })
	p, d := New(), loopbackDescriptor(idp, nil)
	t.Cleanup(p.Close)

	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
	if !ch.Poll || ch.IntervalSec != 1 || ch.URL == "" || opened != ch.URL {
		t.Fatalf("challenge %+v, opened %q", ch, opened)
	}
	if strings.Contains(string(res.Pending), "alice") || len(res.Pending) == 0 {
		t.Fatalf("pending %s", res.Pending)
	}
	u, _ := url.Parse(ch.URL)
	if !strings.HasPrefix(u.Query().Get("redirect_uri"), "http://localhost:") {
		t.Fatalf("redirect %q", u.Query().Get("redirect_uri"))
	}

	// the browser has not come back: still waiting, same state
	w, err := poll(p, env, d, res)
	if err != nil || w.Challenge == nil || w.Token != nil || string(w.Pending) != string(res.Pending) {
		t.Fatalf("poll before the answer: %+v %v", w, err)
	}

	if err := br(ch.URL); err != nil {
		t.Fatal(err)
	}
	done, err := poll(p, env, d, res)
	tok := mustToken(t, done, err)
	if tok.Principal != "sub-alice" {
		t.Fatalf("principal %q", tok.Principal)
	}
	if sv, err := parseSaved(done.Save); err != nil || sv.RefreshToken == "" || sv.Type != savedRefresh {
		t.Fatalf("saved %s %v", done.Save, err)
	}
	// the answer is taken once: the listener is gone and the request cannot be completed again
	if _, err := poll(p, env, d, res); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("second poll: %v", err)
	}
}

func TestLoopbackListenerRules(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, d := New(), loopbackDescriptor(idp, nil)
	t.Cleanup(p.Close)
	env := loopbackEnv(t, idp, func(string) error { return nil })
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
	u, _ := url.Parse(ch.URL)
	back, _ := url.Parse(u.Query().Get("redirect_uri"))
	base := "http://127.0.0.1:" + back.Port()

	get := func(path, host string) int {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	state := u.Query().Get("state")
	if c := get("/?code=x&state=wrong", ""); c != http.StatusBadRequest {
		t.Errorf("a wrong state: %d", c)
	}
	if c := get("/?code=x&state="+state, "evil.example.com"); c != http.StatusMisdirectedRequest {
		t.Errorf("a foreign Host: %d", c)
	}
	if c := get("/other?code=x&state="+state, ""); c != http.StatusNotFound {
		t.Errorf("another path: %d", c)
	}
	// none of that ended the request
	w, err := poll(p, env, d, res)
	if err != nil || w.Token != nil || w.Challenge == nil {
		t.Fatalf("stray requests must not end the sign-in: %+v %v", w, err)
	}
	// the first valid answer wins; a later one is ignored
	if c := get("/?code=first&state="+state, ""); c != http.StatusOK {
		t.Errorf("valid answer: %d", c)
	}
	get("/?code=second&state="+state, "")
	_, err = poll(p, env, d, res)
	if !errors.Is(err, credential.ErrInvalidGrant) { // "first" is not a code the provider issued
		t.Fatalf("got %v", err)
	}
	if got := idp.forms[len(idp.forms)-1].Get("code"); got != "first" {
		t.Fatalf("the first answer must win, the exchange used %q", got)
	}
}

func TestLoopbackErrors(t *testing.T) {
	cases := map[string]struct {
		idp  func(*fakeIdP)
		hit  func(authURL string, idp *fakeIdP) string // builds the redirect the browser follows
		want error
	}{
		"access denied": {nil, func(a string, i *fakeIdP) string {
			return redirectWith(i.authorize(a, "alice"), "error", "access_denied", "code", "")
		}, errDenied},
		"foreign issuer": {func(i *fakeIdP) { i.issParam = true }, func(a string, i *fakeIdP) string {
			return redirectWith(i.authorize(a, "alice"), "iss", "https://evil.example.com")
		}, errMixup},
		"issuer missing": {func(i *fakeIdP) { i.issParam = true }, func(a string, i *fakeIdP) string {
			return redirectWith(i.authorize(a, "alice"), "iss", "")
		}, errMixup},
		"nonce replaced": {func(i *fakeIdP) { i.nonceOverride = "another" }, func(a string, i *fakeIdP) string { return i.authorize(a, "alice") }, errNonce},
	}
	for name, tc := range cases {
		idp := newFakeIdP(t, false)
		if tc.idp != nil {
			tc.idp(idp)
		}
		var authURL string
		env := loopbackEnv(t, idp, func(u string) error { authURL = u; return nil })
		p, d := New(), loopbackDescriptor(idp, nil)
		res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
		mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
		resp, err := http.Get(tc.hit(authURL, idp))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
		_, err = poll(p, env, d, res)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
		if name != "nonce replaced" && !errors.Is(err, credential.ErrInvalidGrant) {
			t.Errorf("%s: the request must end (ErrInvalidGrant), got %v", name, err)
		}
		p.Close()
	}
}

func redirectWith(raw string, kv ...string) string {
	u, _ := url.Parse(raw)
	q := u.Query()
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "" {
			q.Del(kv[i])
		} else {
			q.Set(kv[i], kv[i+1])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func TestLoopbackCancelAndRestart(t *testing.T) {
	idp := newFakeIdP(t, false)
	env := loopbackEnv(t, idp, func(string) error { return nil })
	d := loopbackDescriptor(idp, map[string]string{"listen-address": "127.0.0.1:0", "skip-open-browser": "true"})
	opened := false
	env.OpenURL = func(string) error { opened = true; return nil }
	p := New()
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
	if opened {
		t.Fatal("skip-open-browser must not open the browser")
	}
	p.Cancel(res.Pending)
	if _, err := poll(p, env, d, res); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("after Cancel: %v", err)
	}
	// a restarted process has no listener
	res, err = p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
	if _, err := poll(New(), env, d, res); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("another process: %v", err)
	}
	p.Close()
}

func TestLoopbackPortInUseFallsBack(t *testing.T) {
	idp := newFakeIdP(t, false)
	env := loopbackEnv(t, idp, func(string) error { return nil })
	p := New()
	t.Cleanup(p.Close)
	d0 := loopbackDescriptor(idp, nil)
	first, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d0, Subject: subjectUser()})
	ch := mustChallenge(t, first, err, credential.ChallengeBrowserLoopback)
	u, _ := url.Parse(ch.URL)
	back, _ := url.Parse(u.Query().Get("redirect_uri"))
	d := loopbackDescriptor(idp, map[string]string{"listen-address": "127.0.0.1:" + back.Port() + ",127.0.0.1:0"})
	second, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch2 := mustChallenge(t, second, err, credential.ChallengeBrowserLoopback)
	u2, _ := url.Parse(ch2.URL)
	back2, _ := url.Parse(u2.Query().Get("redirect_uri"))
	if back2.Port() == back.Port() {
		t.Fatal("the busy port must be skipped")
	}
	d = loopbackDescriptor(idp, map[string]string{"listen-address": "127.0.0.1:" + back.Port()})
	if _, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()}); !errors.Is(err, errListen) {
		t.Fatalf("all addresses busy: %v", err)
	}
}

func TestPasteRedirect(t *testing.T) {
	for _, desktop := range []bool{true, false} {
		idp := newFakeIdP(t, !desktop)
		idp.issParam = true
		env := idp.env(desktop)
		p, ctx := New(), context.Background()
		d := idp.descriptor("authcode-keyboard", nil)
		begin := func() (credential.Result, credential.Challenge) {
			res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
			ch := mustChallenge(t, res, err, credential.ChallengePasteRedirect)
			return res, ch
		}
		answer := func(res credential.Result, ch credential.Challenge, pasted string) (credential.Result, error) {
			return p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: ch, Pending: res.Pending,
				Answer: credential.Answer{"redirectUrl": pasted}})
		}

		res, ch := begin()
		if len(ch.Fields) != 1 || ch.Fields[0].Name != "redirectUrl" || !ch.Fields[0].Secret {
			t.Fatalf("fields %+v", ch.Fields)
		}
		good := idp.authorize(ch.URL, "alice")
		if !strings.HasPrefix(good, "http://localhost:8000/") {
			t.Fatalf("the default redirect is http://localhost:8000, got %q", good)
		}

		for name, tc := range map[string]struct {
			bad  string
			want error
		}{
			"another host": {strings.Replace(good, "localhost:8000", "evil.example.com", 1), credential.ErrInvalidAnswer},
			"another path": {strings.Replace(good, "8000/", "8000/other", 1), credential.ErrInvalidAnswer},
			"wrong state":  {redirectWith(good, "state", "forged"), credential.ErrAnswerRejected},
			"wrong issuer": {redirectWith(good, "iss", "https://evil.example.test"), credential.ErrAnswerRejected},
			"no code":      {redirectWith(good, "code", ""), credential.ErrInvalidAnswer},
			"not a url":    {"%%%", credential.ErrInvalidAnswer},
			"empty":        {"", credential.ErrInvalidAnswer},
			"too long":     {good + strings.Repeat("a", maxPastedURL), credential.ErrInvalidAnswer},
		} {
			if _, err := answer(res, ch, tc.bad); !errors.Is(err, tc.want) {
				t.Errorf("%s: got %v, want %v", name, err, tc.want)
			}
		}
		if n := idp.hit("/token"); n != 0 {
			t.Fatalf("rejected answers must not reach the token endpoint (%d)", n)
		}
		// a response of another issuer ends the request
		if _, err := answer(res, ch, redirectWith(good, "iss", "https://evil.example.com")); !errors.Is(err, errMixup) || !errors.Is(err, credential.ErrInvalidGrant) {
			t.Errorf("mix-up: %v", err)
		}
		if _, err := answer(res, ch, redirectWith(good, "iss", "")); !errors.Is(err, errMixup) {
			t.Errorf("missing iss: %v", err)
		}
		if _, err := answer(res, ch, redirectWith(good, "error", "access_denied", "code", "")); !errors.Is(err, errDenied) {
			t.Errorf("denied: %v", err)
		}

		done, err := answer(res, ch, good)
		mustToken(t, done, err)
		// the code works once
		if _, err := answer(res, ch, good); !errors.Is(err, credential.ErrInvalidGrant) {
			t.Errorf("replay: %v", err)
		}
		// state of an earlier request does not fit a new one
		res2, ch2 := begin()
		if _, err := answer(res2, ch2, good); !errors.Is(err, credential.ErrAnswerRejected) {
			t.Errorf("state of another request: %v", err)
		}
	}
}

func TestPasteRedirectRejectsStateReplayAcrossChallenges(t *testing.T) {
	idp := newFakeIdP(t, false)
	idp.issParam = true
	env, ctx := idp.env(true), context.Background()
	p := New()
	d := idp.descriptor("authcode-keyboard", nil)
	first, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	firstChallenge := mustChallenge(t, first, err, credential.ChallengePasteRedirect)
	second, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	secondChallenge := mustChallenge(t, second, err, credential.ChallengePasteRedirect)

	firstURL, err := url.Parse(firstChallenge.URL)
	if err != nil {
		t.Fatal(err)
	}
	secondURL, err := url.Parse(secondChallenge.URL)
	if err != nil {
		t.Fatal(err)
	}
	if firstURL.Query().Get("state") == secondURL.Query().Get("state") {
		t.Fatal("authorization challenges must use independent state values")
	}

	replayed := idp.authorize(firstChallenge.URL, "alice")
	_, err = p.Continue(ctx, env, credential.Request{
		Descriptor: d, Subject: subjectUser(), Challenge: secondChallenge, Pending: second.Pending,
		Answer: credential.Answer{"redirectUrl": replayed},
	})
	if !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("a callback from another challenge must be rejected: %v", err)
	}
}

func TestPasteRedirectPortAndHostFromKubeconfig(t *testing.T) {
	idp := newFakeIdP(t, false)
	d := idp.descriptor("authcode-keyboard", map[string]string{"listen-address": "127.0.0.1:9100", "redirect-url-hostname": "dev.local"})
	res, err := New().Begin(context.Background(), idp.env(true), credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengePasteRedirect)
	u, _ := url.Parse(ch.URL)
	if got := u.Query().Get("redirect_uri"); got != "http://dev.local:9100" {
		t.Fatalf("redirect_uri %q", got)
	}
	if u.Query().Get("code_challenge_method") != "S256" || len(u.Query().Get("state")) < 16 || len(u.Query().Get("nonce")) < 16 {
		t.Fatalf("authorization request %v", u.Query())
	}
}

func TestAuthorizationRequestParameters(t *testing.T) {
	idp := newFakeIdP(t, false)
	d := idp.descriptor("authcode-keyboard", map[string]string{"scope": "groups", "auth-request-extra-params": "audience=api&state=forged"})
	res, err := New().Begin(context.Background(), idp.env(true), credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengePasteRedirect)
	q, _ := url.Parse(ch.URL)
	v := q.Query()
	if v.Get("scope") != "openid groups" || v.Get("audience") != "api" || v.Get("state") == "forged" || v.Get("client_id") != testClient {
		t.Fatalf("%v", v)
	}
}

// The listener of the desktop is released as soon as the request is cancelled (the Broker calls Cancel with the
// Pending of the request): the port refuses connections at once, it does not wait for loopbackTTL.
func TestLoopbackListenerIsClosedRightAfterCancel(t *testing.T) {
	idp := newFakeIdP(t, false)
	env := loopbackEnv(t, idp, func(string) error { return nil })
	d := loopbackDescriptor(idp, map[string]string{"listen-address": "127.0.0.1:0", "skip-open-browser": "true"})
	p := New()
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
	authURL, err := url.Parse(ch.URL)
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(authURL.Query().Get("redirect_uri"))
	if err != nil || redirect.Port() == "" {
		t.Fatalf("no redirect_uri with a port in %q: %v", ch.URL, err)
	}
	addr := net.JoinHostPort("127.0.0.1", redirect.Port())

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("the listener must accept connections while the request is open: %v", err)
	}
	_ = conn.Close()

	var canceller credential.Canceller = p // the optional interface the Broker looks for
	canceller.Cancel(res.Pending)
	if conn, err := net.Dial("tcp", addr); err == nil {
		_ = conn.Close()
		t.Fatal("the listener is still open after Cancel")
	}
}

func TestLoopbackCloseReleasesEveryListener(t *testing.T) {
	idp := newFakeIdP(t, false)
	env := loopbackEnv(t, idp, func(string) error { return nil })
	d := loopbackDescriptor(idp, map[string]string{"listen-address": "127.0.0.1:0", "skip-open-browser": "true"})
	p := New()
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeBrowserLoopback)
	authURL, _ := url.Parse(ch.URL)
	redirect, _ := url.Parse(authURL.Query().Get("redirect_uri"))
	addr := net.JoinHostPort("127.0.0.1", redirect.Port())
	var closer credential.Closer = p
	closer.Close()
	if conn, err := net.Dial("tcp", addr); err == nil {
		_ = conn.Close()
		t.Fatal("the listener is still open after Close")
	}
}
