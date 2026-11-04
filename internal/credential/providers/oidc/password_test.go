// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func TestPasswordUserScope(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := idp.descriptor("password", nil)

	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeCredentials)
	if len(ch.Fields) != 2 || ch.Fields[0].Name != "username" || ch.Fields[0].Secret || ch.Fields[1].Name != "password" || !ch.Fields[1].Secret {
		t.Fatalf("fields: %+v", ch.Fields)
	}
	cont := func(user, pw string) (credential.Result, error) {
		return p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: ch,
			Answer: credential.Answer{"username": user, "password": pw}})
	}

	_, err = cont("alice", "wrong")
	if !errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("a wrong password must be ErrAnswerRejected, got %v", err)
	}
	if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "Invalid user") {
		t.Fatalf("the provider's explanation leaked: %v", err)
	}

	res, err = cont("alice", idp.users["alice"])
	tok := mustToken(t, res, err)
	if tok.Principal != "sub-alice" || tok.Value != idp.lastIDToken || !tok.Expiry.After(idp.clock.Now()) {
		t.Fatalf("token: %+v", tok)
	}
	// the personal password is never saved: only the refresh token
	if strings.Contains(string(res.Save), idp.users["alice"]) || strings.Contains(string(res.Save), "alice") {
		t.Fatalf("Save must not hold the password or the login: %s", res.Save)
	}
	sv, err := parseSaved(res.Save)
	if err != nil || sv.Type != savedRefresh || sv.V != 1 || sv.RefreshToken != idp.lastRefresh {
		t.Fatalf("saved: %+v %v", sv, err)
	}

	// a later Begin uses the refresh token, rotates it and asks nothing
	res2, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	mustToken(t, res2, err)
	sv2, _ := parseSaved(res2.Save)
	if sv2 == nil || sv2.RefreshToken == sv.RefreshToken || sv2.RefreshToken != idp.lastRefresh {
		t.Fatalf("the rotated refresh token must be saved: %+v", sv2)
	}

	// the first refresh token is used up: its holder is asked again and the record is erased
	res3, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	mustChallenge(t, res3, err, credential.ChallengeCredentials)
	if res3.Save == nil || len(res3.Save) != 0 {
		t.Fatalf("unusable saved data must be erased with an empty non-nil Save, got %#v", res3.Save)
	}
}

func TestPasswordTeamScopeSignsInAgain(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := idp.descriptor("password", nil)
	sub := subjectTeam()

	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: sub})
	ch := mustChallenge(t, res, err, credential.ChallengeCredentials)
	res, err = p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: sub, Challenge: ch,
		Answer: credential.Answer{"username": "alice", "password": idp.users["alice"]}})
	mustToken(t, res, err)
	sv, err := parseSaved(res.Save)
	if err != nil || sv.Type != savedPassword || sv.Username != "alice" || sv.Password != idp.users["alice"] || sv.RefreshToken == "" {
		t.Fatalf("a team keeps login, password and the refresh token: %+v %v", sv, err)
	}

	// the refresh token dies; the saved password signs in again without a question
	idp.mu.Lock()
	idp.refresh = map[string]string{}
	idp.mu.Unlock()
	res2, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: sub, Saved: res.Save})
	mustToken(t, res2, err)
	sv2, _ := parseSaved(res2.Save)
	if sv2 == nil || sv2.Password != idp.users["alice"] || sv2.RefreshToken != idp.lastRefresh {
		t.Fatalf("saved after re-login: %+v", sv2)
	}

	// the password was changed at the provider: the saved sign-in is erased and the user is asked
	idp.mu.Lock()
	idp.users["alice"] = "new-password"
	idp.refresh = map[string]string{}
	idp.mu.Unlock()
	res3, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: sub, Saved: res2.Save})
	mustChallenge(t, res3, err, credential.ChallengeCredentials)
	if res3.Save == nil || len(res3.Save) != 0 {
		t.Fatalf("expected the record to be erased, got %#v", res3.Save)
	}
}

// The record written by ticket 3.7 (no version) is read as it is, even when the descriptor knows nothing
// about the provider, and is replaced by a version 1 record that keeps every field.
func TestSavedFormatOfTicket37(t *testing.T) {
	idp := newFakeIdP(t, false)
	idp.secret = testSecret
	legacy, _ := json.Marshal(map[string]any{"issuerUrl": idp.issuer(), "clientId": testClient, "clientSecret": testSecret,
		"scopes": []string{"openid", "groups"}, "username": "alice", "password": idp.users["alice"]})
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"grant": "password"}}

	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectTeam(), Saved: legacy})
	tok := mustToken(t, res, err)
	if tok.Principal != "sub-alice" {
		t.Fatalf("principal %q", tok.Principal)
	}
	sv, err := parseSaved(res.Save)
	if err != nil || sv.V != 1 || sv.Type != savedPassword || sv.ClientSecret != testSecret || sv.IssuerURL != idp.issuer() ||
		sv.Username != "alice" || sv.RefreshToken == "" {
		t.Fatalf("upgraded record: %+v %v", sv, err)
	}
	last := idp.forms[len(idp.forms)-1]
	if last.Get("scope") != "openid groups" {
		t.Fatalf("scopes of the 3.7 record must be used, got %q", last.Get("scope"))
	}
}

func TestSavedParsing(t *testing.T) {
	for name, in := range map[string]string{
		"garbage":         `not json`,
		"future version":  `{"v":2,"type":"refresh","refreshToken":"x"}`,
		"empty object":    `{}`,
		"unknown type":    `{"v":1,"type":"magic"}`,
		"refresh no data": `{"v":1,"type":"refresh"}`,
		"password no pw":  `{"v":1,"type":"password","username":"a"}`,
	} {
		if sv, err := parseSaved([]byte(in)); err == nil {
			t.Errorf("%s: accepted: %+v", name, sv)
		}
	}
	if sv, err := parseSaved(nil); sv != nil || err != nil {
		t.Errorf("nil input: %v %v", sv, err)
	}
	// unreadable saved data: asked again and erased
	idp := newFakeIdP(t, false)
	res, err := New().Begin(context.Background(), idp.env(true), credential.Request{Descriptor: idp.descriptor("password", nil), Subject: subjectUser(), Saved: []byte(`{"v":9}`)})
	mustChallenge(t, res, err, credential.ChallengeCredentials)
	if res.Save == nil || len(res.Save) != 0 {
		t.Fatalf("got %#v", res.Save)
	}
}

func TestPasswordFromKubeconfigArguments(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := idp.descriptor("password", map[string]string{"username": "alice", "password": idp.users["alice"]})
	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustToken(t, res, err)

	d = idp.descriptor("password", map[string]string{"username": "alice", "password": "wrong"})
	res, err = p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustChallenge(t, res, err, credential.ChallengeCredentials) // refused: the user is asked
}

func TestClientSecretPlacement(t *testing.T) {
	for _, tc := range []struct {
		methods   []string
		wantBasic bool
	}{{nil, true}, {[]string{"client_secret_basic", "client_secret_post"}, true}, {[]string{"client_secret_post"}, false}} {
		idp := newFakeIdP(t, false)
		idp.secret, idp.authMethods = testSecret, tc.methods
		p, ctx, env := New(), context.Background(), idp.env(true)
		d := idp.descriptor("password", map[string]string{"username": "alice", "password": idp.users["alice"]})
		res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
		mustToken(t, res, err)
		if idp.sawBasic != tc.wantBasic {
			t.Errorf("methods %v: basic=%v, want %v", tc.methods, idp.sawBasic, tc.wantBasic)
		}
		if tc.wantBasic && idp.forms[0].Get("client_secret") != "" {
			t.Errorf("the secret must not be in the body together with basic authentication")
		}
	}
}

func TestPasswordBadAnswers(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := idp.descriptor("password", nil)
	ch := credential.Challenge{Kind: credential.ChallengeCredentials}
	for _, a := range []credential.Answer{{}, {"username": "alice"}, {"password": "x"}, {"username": "alice", "password": strings.Repeat("x", 5000)}} {
		_, err := p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: ch, Answer: a})
		if !errors.Is(err, credential.ErrInvalidAnswer) || errors.Is(err, credential.ErrAnswerRejected) {
			t.Errorf("%v: got %v, want ErrInvalidAnswer", a, err)
		}
	}
	if n := idp.hit("/token"); n != 0 {
		t.Errorf("incomplete answers must not reach the provider (%d requests)", n)
	}
}

// An identity provider that is down (a server error, throttling, no connection) is "the service is unavailable",
// never "wrong password": the user is not told to change a correct password.
func TestPasswordOutageIsNotAWrongPassword(t *testing.T) {
	for name, status := range map[string]int{"server error": 500, "bad gateway": 502, "unavailable": 503, "throttled": 429} {
		idp := newFakeIdP(t, false)
		p, ctx, env := New(), context.Background(), idp.env(true)
		d := idp.descriptor("password", nil)
		res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
		ch := mustChallenge(t, res, err, credential.ChallengeCredentials)
		idp.mu.Lock()
		idp.tokenStatus = status
		idp.mu.Unlock()
		_, err = p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: ch,
			Answer: credential.Answer{"username": "alice", "password": idp.users["alice"]}})
		if !errors.Is(err, credential.ErrServiceUnavailable) || errors.Is(err, credential.ErrAnswerRejected) {
			t.Errorf("%s (%d): got %v, want ErrServiceUnavailable", name, status, err)
		}
	}
}

func TestPasswordClientErrorsStayRejections(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := idp.descriptor("password", nil)
	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeCredentials)
	_, err = p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: ch,
		Answer: credential.Answer{"username": "alice", "password": "wrong"}})
	if !errors.Is(err, credential.ErrAnswerRejected) || errors.Is(err, credential.ErrServiceUnavailable) {
		t.Fatalf("a wrong password: %v", err)
	}
}

func TestPasswordUnreachableProviderIsUnavailable(t *testing.T) {
	idp := newFakeIdP(t, false)
	p, ctx, env := New(), context.Background(), idp.env(true)
	d := idp.descriptor("password", nil)
	res, err := p.Begin(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeCredentials)
	idp.srv.Close() // nothing listens any more
	_, err = p.Continue(ctx, env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: ch,
		Answer: credential.Answer{"username": "alice", "password": idp.users["alice"]}})
	if !errors.Is(err, credential.ErrServiceUnavailable) || errors.Is(err, credential.ErrAnswerRejected) {
		t.Fatalf("got %v, want ErrServiceUnavailable", err)
	}
}
