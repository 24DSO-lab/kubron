// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// shown collects everything the provider lets out to a place where a secret must not be: error texts (in
// the three usual formattings), the public challenge as the API would send it, tokens and answers printed
// with fmt, and the log. Save and Pending are not "shown": they are the data the Broker encrypts.
type shown struct {
	t *testing.T
	b strings.Builder
}

func (s *shown) err(err error) {
	if err != nil {
		fmt.Fprintf(&s.b, "%v|%+v|%#v\n", err, err, err)
	}
}

func (s *shown) result(res credential.Result, err error) {
	s.err(err)
	if res.Challenge != nil {
		j, _ := json.Marshal(res.Challenge)
		s.b.Write(j)
		s.b.WriteByte('\n')
	}
	if res.Token != nil {
		fmt.Fprintf(&s.b, "%v|%+v|%#v\n", *res.Token, res.Token, *res.Token)
	}
}

func (s *shown) answer(a credential.Answer) { fmt.Fprintf(&s.b, "%v|%+v|%#v\n", a, a, a) }

// The canary test: every secret the flows handle has a recognisable value, all flows run (successes and
// every kind of failure), and none of those values may appear in anything shown.
func TestSecretsAreNotShown(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	wd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	out := &shown{t: t}
	ctx := context.Background()
	idp := newFakeIdP(t, false)
	idp.secret = testSecret
	idp.users["alice"] = "pw-CANARY-9d41"
	p := New()
	t.Cleanup(p.Close)
	env := idp.env(true)
	var authURL string
	env.OpenURL = func(u string) error { authURL = u; return nil }

	// password: refused, accepted (team keeps the password in Save), refresh, dead refresh token
	d := idp.descriptor("password", nil)
	team := subjectTeam()
	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: team})
	out.result(res, err)
	ch := *res.Challenge
	for _, pw := range []string{"wrong-CANARY-pw", idp.users["alice"]} {
		a := credential.Answer{"username": "alice", "password": pw}
		out.answer(a)
		res, err = p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: team, Challenge: ch, Answer: a})
		out.result(res, err)
	}
	saved := res.Save
	res, err = p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: team, Saved: saved})
	out.result(res, err)
	res, err = p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: []byte(`{"v":1,"type":"refresh","refreshToken":"dead-CANARY-refresh"}`)})
	out.result(res, err)

	// token problems
	for _, set := range []func(*fakeIdP){
		func(f *fakeIdP) { f.badSignature = true },
		func(f *fakeIdP) { f.audOverride = "x" },
		func(f *fakeIdP) { f.noIDToken = true },
	} {
		bad := newFakeIdP(t, false)
		bad.secret = testSecret
		set(bad)
		r, err := p.Begin(ctx, bad.env(true), credential.Request{Descriptor: bad.descriptor("password", map[string]string{"username": "alice", "password": bad.users["alice"]}), Subject: subjectUser()})
		out.result(r, err)
		idp.forms = append(idp.forms, bad.forms...) // the strings of that provider count too
		idp.mu.Lock()
		idp.users["x-"+bad.lastRefresh] = bad.lastIDToken // keeps them in the list below
		idp.mu.Unlock()
	}

	// authorization code with a listener
	ld := idp.descriptor("authcode", map[string]string{"listen-address": "127.0.0.1:0"})
	res, err = p.Begin(ctx, env, credential.Request{Descriptor: ld, Subject: subjectUser()})
	out.result(res, err)
	lres := res
	resp, err := http.Get(redirectWith(idp.authorize(authURL, "alice"), "iss", "https://evil.example.com"))
	if err == nil {
		resp.Body.Close()
	}
	r, err := poll(p, env, ld, lres)
	out.result(r, err)

	// paste: wrong state, mix-up, a good answer, a replay
	pd := idp.descriptor("authcode-keyboard", nil)
	res, err = p.Begin(ctx, env, credential.Request{Descriptor: pd, Subject: subjectUser()})
	out.result(res, err)
	good := idp.authorize(res.Challenge.URL, "alice")
	for _, pasted := range []string{redirectWith(good, "state", "forged"), redirectWith(good, "iss", "https://evil.example.com"), good, good, "%%%"} {
		a := credential.Answer{"redirectUrl": pasted}
		out.answer(a)
		r, err := p.Continue(ctx, env, credential.Request{Descriptor: pd, Subject: subjectUser(), Challenge: *res.Challenge, Pending: res.Pending, Answer: a})
		out.result(r, err)
	}

	// device: pending, denied
	dd := idp.descriptor("device-code", nil)
	denv := idp.env(true)
	res, err = p.Begin(ctx, denv, credential.Request{Descriptor: dd, Subject: subjectUser()})
	out.result(res, err)
	idp.approveDevice("", true)
	idp.clock.Advance(6 * time.Second)
	r, err = p.Continue(ctx, denv, credential.Request{Descriptor: dd, Subject: subjectUser(), Challenge: *res.Challenge, Pending: res.Pending})
	out.result(r, err)

	// an unreachable provider
	dead := newFakeIdP(t, false)
	dd2 := dead.descriptor("password", map[string]string{"username": "alice", "password": "pw-CANARY-9d41"})
	dead.srv.Close()
	r, err = p.Begin(ctx, dead.env(true), credential.Request{Descriptor: dd2, Subject: subjectUser()})
	out.result(r, err)

	// what must not appear
	forbidden := map[string]string{
		"user password": idp.users["alice"], "wrong password": "wrong-CANARY-pw", "client secret": testSecret,
		"dead refresh token": "dead-CANARY-refresh", "last ID token": idp.lastIDToken, "last access token": idp.lastAccess,
		"last refresh token": idp.lastRefresh,
	}
	for _, f := range idp.forms {
		for _, k := range []string{"code", "code_verifier", "device_code", "refresh_token", "password", "client_secret"} {
			if v := f.Get(k); len(v) > 6 {
				forbidden[k+" sent to the provider"] = v
			}
		}
	}
	for _, v := range idp.users {
		if strings.Contains(v, ".") && len(v) > 40 { // ID tokens of the extra providers
			forbidden["ID token of another provider"] = v
		}
	}
	for _, v := range []string{url.QueryEscape(idp.users["alice"]), strings.ToUpper(idp.users["alice"])} {
		forbidden["encoded password "+v] = v
	}
	hay := out.b.String() + logs.String()
	for what, v := range forbidden {
		if v != "" && strings.Contains(hay, v) {
			t.Errorf("%s (%q...) was shown", what, v[:min(len(v), 12)])
		}
	}
	if len(forbidden) < 12 {
		t.Errorf("the canary list is too short (%d): the flows did not run", len(forbidden))
	}
	if logs.Len() != 0 {
		t.Errorf("the provider must not log, got: %s", logs.String())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("the provider wrote files into the working directory: %v", ents)
	}
}

// The Token and Answer types hide their values from fmt, which is what keeps them out of logs.
func TestRedactedTypes(t *testing.T) {
	tok := credential.Token{Value: "CANARY-token"}
	ans := credential.Answer{"password": "CANARY-password"}
	for _, s := range []string{fmt.Sprint(tok), fmt.Sprintf("%+v", tok), fmt.Sprintf("%#v", tok), fmt.Sprint(ans), fmt.Sprintf("%#v", ans), fmt.Sprintf("%v", &tok)} {
		if strings.Contains(s, "CANARY") {
			t.Errorf("leaked: %s", s)
		}
	}
}
