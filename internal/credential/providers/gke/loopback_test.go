// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
)

type desktopRun struct {
	p      *Provider
	g      *userGoogle
	env    credential.Env
	c      credential.Challenge
	pend   []byte
	pd     pending
	addr   string // host:port of the listener
	mu     sync.Mutex
	opened []string
}

func (d *desktopRun) openedURLs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.opened...)
}

func startDesktop(t *testing.T, openErr error) *desktopRun {
	t.Helper()
	d := &desktopRun{g: newUserGoogle(t), p: userProvider(t, "")}
	d.env = envFor(d.g.google)
	d.env.Desktop = true
	d.env.OpenURL = func(u string) error {
		d.mu.Lock()
		d.opened = append(d.opened, u)
		d.mu.Unlock()
		return openErr
	}
	t.Cleanup(d.p.Close)
	d.c, d.pend, d.pd = begin(t, d.p, d.env)
	u, err := url.Parse(d.pd.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	d.addr = u.Host
	d.g.nonce = d.pd.Nonce
	return d
}

func (d *desktopRun) poll(ctx context.Context) (credential.Result, error) {
	return d.p.Continue(ctx, d.env, answerReq(d.c, d.pend, ""))
}

// get sends a request to the listener as a browser would; host overrides the Host header when not empty.
func (d *desktopRun) get(t *testing.T, method, path, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+d.addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	// The test's browser has a transport of its own: the default one is trapped by newGoogle.
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (d *desktopRun) back(extra string) string {
	return "/?state=" + url.QueryEscape(d.pd.State) + extra
}

func listening(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func TestLoopbackStartsListenerAndOpensTheBrowser(t *testing.T) {
	d := startDesktop(t, nil)
	if d.c.Kind != credential.ChallengeBrowserLoopback || !d.c.Poll || d.c.IntervalSec != 1 || len(d.c.Fields) != 0 {
		t.Fatalf("challenge = %+v", d.c)
	}
	u, _ := url.Parse(d.pd.RedirectURI)
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" {
		t.Fatalf("redirect = %q", d.pd.RedirectURI)
	}
	if !listening(d.addr) || d.p.loops.count() != 1 {
		t.Fatal("the listener must be open")
	}
	if got := d.openedURLs(); len(got) != 1 || got[0] != d.c.URL {
		t.Fatalf("opened = %v", got)
	}
	if q, _ := url.Parse(d.c.URL); q.Query().Get("redirect_uri") != d.pd.RedirectURI {
		t.Fatal("the address does not carry the redirect")
	}
	if d.g.callCount() != 0 {
		t.Fatal("starting needs no request")
	}
}

func TestLoopbackEndToEnd(t *testing.T) {
	d := startDesktop(t, nil)
	res, err := d.poll(context.Background())
	if err != nil || res.Challenge == nil || res.Token != nil || res.Challenge.ID != "" || !res.Challenge.ExpiresAt.IsZero() || string(res.Pending) != string(d.pend) {
		t.Fatalf("a poll before the browser returned must wait unchanged: %+v %v", res, err)
	}
	status, body := d.get(t, "GET", d.back("&code=4%2F0AbCd-code"), "")
	if status != 200 || !strings.Contains(body, "Sign-in complete") || strings.Contains(body, "4/0AbCd") {
		t.Fatalf("browser page: %d %q", status, body)
	}
	res, err = d.poll(context.Background())
	if err != nil || res.Token == nil || res.Token.Principal != testUserEmail || res.Pending != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if sv, ok := parseSavedUser(res.Save); !ok || sv.RefreshToken != testRefresh {
		t.Fatal("the refresh token must be saved")
	}
	calls, urls := d.g.snapshot()
	if len(calls) != 1 || urls[0] != TokenURL || calls[0].Form.Get("redirect_uri") != d.pd.RedirectURI ||
		calls[0].Form.Get("code") != "4/0AbCd-code" || calls[0].Form.Get("code_verifier") != d.pd.Verifier {
		t.Fatalf("exchange: %v %v", urls, calls)
	}
	if listening(d.addr) || d.p.loops.count() != 0 {
		t.Fatal("the listener must be closed once the answer is taken")
	}
	if _, err := d.poll(context.Background()); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("the answer is taken exactly once: err = %v", err)
	}
}

func TestLoopbackIgnoresWhatIsNotTheBrowserOfThisRequest(t *testing.T) {
	d := startDesktop(t, nil)
	for name, tc := range map[string]struct {
		method, path, host string
		want               int
	}{
		"wrong state":    {"GET", "/?state=other&code=x", "", 400},
		"no state":       {"GET", "/?code=x", "", 400},
		"foreign host":   {"GET", d.back("&code=x"), "evil.example", 421},
		"localhost name": {"GET", d.back("&code=x"), "localhost", 421},
		"other path":     {"GET", "/callback?state=" + url.QueryEscape(d.pd.State) + "&code=x", "", 404},
		"post":           {"POST", d.back("&code=x"), "", 404},
	} {
		status, body := d.get(t, tc.method, tc.path, tc.host)
		if status != tc.want {
			t.Errorf("%s: status %d, want %d", name, status, tc.want)
		}
		if strings.Contains(body, d.pd.State) || strings.Contains(body, "code=x") {
			t.Errorf("%s: the answer echoes the request: %q", name, body)
		}
	}
	res, err := d.poll(context.Background())
	if err != nil || res.Challenge == nil || res.Token != nil {
		t.Fatalf("nothing of that must count as an answer: %+v %v", res, err)
	}
	if d.g.callCount() != 0 {
		t.Fatal("no request for a rejected visit")
	}
	// The right answer still works afterwards.
	if status, _ := d.get(t, "GET", d.back("&code=good"), ""); status != 200 {
		t.Fatalf("status %d", status)
	}
	if res, err := d.poll(context.Background()); err != nil || res.Token == nil {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestLoopbackFirstAnswerWins(t *testing.T) {
	d := startDesktop(t, nil)
	d.get(t, "GET", d.back("&code=first"), "")
	d.get(t, "GET", d.back("&code=second"), "")
	if _, err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, _ := d.g.snapshot()
	if len(calls) != 1 || calls[0].Form.Get("code") != "first" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestLoopbackDeniedByTheUserEndsTheRequest(t *testing.T) {
	d := startDesktop(t, nil)
	d.get(t, "GET", d.back("&error=access_denied"), "")
	_, err := d.poll(context.Background())
	if !errors.Is(err, credential.ErrInvalidGrant) {
		t.Fatalf("err = %v", err)
	}
	if listening(d.addr) {
		t.Fatal("the listener must be closed")
	}
	if d.g.callCount() != 0 {
		t.Fatal("no request")
	}
}

func TestLoopbackIDTokenIsStillChecked(t *testing.T) {
	d := startDesktop(t, nil)
	d.g.override = func(c map[string]any) { c["nonce"] = "another" }
	d.get(t, "GET", d.back("&code=x"), "")
	if _, err := d.poll(context.Background()); !errors.Is(err, credential.ErrInvalidGrant) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoopbackIsReleasedByCancelCloseAndTimeout(t *testing.T) {
	t.Run("Cancel", func(t *testing.T) {
		d := startDesktop(t, nil)
		d.p.Cancel(d.pend)
		d.p.Cancel(d.pend) // idempotent
		d.p.Cancel([]byte("garbage"))
		if listening(d.addr) || d.p.loops.count() != 0 {
			t.Fatal("the port must refuse connections at once")
		}
		if _, err := d.poll(context.Background()); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("Close", func(t *testing.T) {
		a, b := startDesktop(t, nil), startDesktop(t, nil)
		a.p.Close()
		if listening(a.addr) || a.p.loops.count() != 0 {
			t.Fatal("Close must release the listener")
		}
		b.p.Close()
		b.p.Close()
		if listening(b.addr) {
			t.Fatal("Close must release the listener")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		g := newUserGoogle(t)
		p := userProvider(t, "")
		p.ttl = 80 * time.Millisecond
		env := envFor(g.google)
		env.Desktop = true
		t.Cleanup(p.Close)
		_, _, pd := begin(t, p, env)
		u, _ := url.Parse(pd.RedirectURI)
		deadline := time.Now().Add(3 * time.Second)
		for listening(u.Host) {
			if time.Now().After(deadline) {
				t.Fatal("the listener outlived its time")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

func TestLoopbackForgottenByARestartedProcess(t *testing.T) {
	d := startDesktop(t, nil)
	other := userProvider(t, "") // a provider that does not know the listener
	_, err := other.Continue(context.Background(), d.env, answerReq(d.c, d.pend, ""))
	if !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoopbackOpenFailureIsNotFatal(t *testing.T) {
	d := startDesktop(t, errors.New("no browser"))
	if d.c.URL == "" || !listening(d.addr) {
		t.Fatal("the challenge must carry the address for the user and the listener must stay open")
	}
}

func TestLoopbackWithoutOpenURLStillWorks(t *testing.T) {
	g := newUserGoogle(t)
	p := userProvider(t, "")
	t.Cleanup(p.Close)
	env := envFor(g.google)
	env.Desktop = true
	c, _, _ := begin(t, p, env)
	if c.Kind != credential.ChallengeBrowserLoopback {
		t.Fatalf("kind = %q", c.Kind)
	}
}

func TestLoopbackSecretsAreNotInTheChallengeOrErrors(t *testing.T) {
	d := startDesktop(t, nil)
	var sh shown
	j := d.c
	sh.result(credential.Result{Challenge: &j}, nil)
	d.get(t, "GET", d.back("&code=CANARY-LOOP-CODE"), "")
	d.g.override = func(c map[string]any) { c["nonce"] = "wrong" }
	sh.result(d.poll(context.Background()))
	out := sh.b.String()
	for _, s := range []string{d.pd.Verifier, testClientSecret, "CANARY-LOOP-CODE"} {
		if strings.Contains(out, s) {
			t.Errorf("a secret shows up: %.20q", s)
		}
	}
}
