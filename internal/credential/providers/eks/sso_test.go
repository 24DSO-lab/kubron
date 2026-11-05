// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"errors"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

var ssoAnswer = credential.Answer{"eksSsoStartUrl": "https://example.awsapps.com/start", "eksSsoRegion": "eu-west-1", "eksAccountId": "111122223333", "eksRoleName": "Admin"}

// startSSO runs the dialog up to the device code.
func startSSO(t *testing.T, p *Provider, f *fakeAWS, d credential.Descriptor) (credential.Result, credential.Env) {
	t.Helper()
	env := f.env()
	res, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustChallenge(t, res, err, credential.ChallengeSecretFields)
	res, err = step(t, p, env, d, res, nil, credential.Answer{"eksMethod": "SSO"})
	if c := mustChallenge(t, res, err, credential.ChallengeSecretFields); names(c) != "eksSsoStartUrl,eksSsoRegion,eksAccountId,eksRoleName" {
		t.Fatalf("fields %s", names(c))
	}
	res, err = step(t, p, env, d, res, nil, ssoAnswer)
	mustChallenge(t, res, err, credential.ChallengeDeviceCode)
	return res, env
}

func poll(t *testing.T, p *Provider, env credential.Env, d credential.Descriptor, res credential.Result) (credential.Result, error) {
	t.Helper()
	return p.Continue(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: *res.Challenge, Pending: res.Pending})
}

func TestSSODeviceFlow(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	d := desc(nil)
	res, env := startSSO(t, p, f, d)
	c := *res.Challenge
	if c.UserCode != "ABCD-EFGH" || !c.Poll || c.IntervalSec != 5 || c.URL != "https://device.example.com/authorize?user_code=ABCD-EFGH" ||
		!c.ExpiresAt.Equal(f.clock.Now().Add(600*time.Second)) || c.ID != "" {
		t.Fatalf("challenge %+v", c)
	}
	// The endpoints are those of the region the user gave, nothing else is asked.
	for _, h := range f.hosts {
		if h != "oidc.eu-west-1.amazonaws.com" {
			t.Errorf("host %q", h)
		}
	}
	if f.hit("oidc /client/register") != 1 || f.hit("oidc /device_authorization") != 1 {
		t.Errorf("hits %v", f.hits)
	}

	// An early poll is answered without asking the service.
	before := f.total()
	w, err := poll(t, p, env, d, res)
	if err != nil || w.Challenge == nil || w.Token != nil || f.total() != before {
		t.Fatalf("early poll: %v %v (%d new requests)", w, err, f.total()-before)
	}
	// In time: still pending, the interval is kept.
	f.clock.Advance(5 * time.Second)
	w, err = poll(t, p, env, d, res)
	if err != nil || w.Challenge == nil || w.Token != nil || f.hit("oidc /token") != 1 || w.Challenge.IntervalSec != 5 {
		t.Fatalf("pending: %v %v", w, err)
	}
	res.Pending = w.Pending
	// The user approves.
	f.mu.Lock()
	f.deviceStatus = "approved"
	f.mu.Unlock()
	f.clock.Advance(5 * time.Second)
	done, err := poll(t, p, env, d, res)
	tok := mustToken(t, done, err)
	if tok.Principal != f.arns[testTempID] || !tok.Expiry.Equal(f.clock.Now().Add(14*time.Minute)) {
		t.Errorf("principal %q expiry %v", tok.Principal, tok.Expiry)
	}
	if akid, sess := signedWith(t, tok); akid != testTempID || sess != testTempTok {
		t.Errorf("signed with %q %q", akid, sess)
	}
	sv, perr := parseSaved(done.Save)
	if perr != nil || sv.Type != savedSSO || sv.RefreshToken != testSSORef || sv.Creds == nil || sv.Principal != tok.Principal || sv.AccountID != "111122223333" {
		t.Fatalf("saved %v", perr)
	}
	if f.hit("portal /federation/credentials") != 1 || f.hit("sts GetCallerIdentity") != 1 {
		t.Errorf("hits %v", f.hits)
	}

	// Later, the cached role credentials are good: no request at all.
	f.clock.Advance(14 * time.Minute)
	n := f.total()
	r2, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: done.Save})
	if mustToken(t, r2, err); f.total() != n || r2.Save != nil {
		t.Errorf("cached credentials: %d new requests, save %q", f.total()-n, r2.Save)
	}
	// They are nearly over: new role credentials with the still valid access token.
	f.clock.Advance(35 * time.Minute)
	r3, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: done.Save})
	mustToken(t, r3, err)
	if f.hit("portal /federation/credentials") != 2 || f.hit("oidc /token") != 2 || r3.Save == nil { // the access token is still valid: no refresh
		t.Errorf("hits %v", f.hits)
	}
	// The access token is over too: it is renewed with the refresh token (grant refresh_token).
	f.clock.Advance(3 * time.Hour)
	r4, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: r3.Save})
	mustToken(t, r4, err)
	if f.hit("oidc /token") != 3 {
		t.Errorf("no refresh: %v", f.hits)
	}
	// The refresh token is refused: the user signs in again and the dead record is erased.
	f.mu.Lock()
	f.refreshOK = false
	f.mu.Unlock()
	f.clock.Advance(3 * time.Hour)
	r5, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: r4.Save})
	mustChallenge(t, r5, err, credential.ChallengeSecretFields)
	if r5.Save == nil || len(r5.Save) != 0 {
		t.Error("the dead record must be erased")
	}
}

func TestSSOWithoutRefreshToken(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	f.issuedRefr = false
	d := desc(nil)
	res, env := startSSO(t, p, f, d)
	f.mu.Lock()
	f.deviceStatus = "approved"
	f.mu.Unlock()
	f.clock.Advance(5 * time.Second)
	done, err := poll(t, p, env, d, res)
	mustToken(t, done, err)
	// The access token ends and there is nothing to renew it with: signing in again is the answer.
	f.clock.Advance(3 * time.Hour)
	r, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: done.Save})
	mustChallenge(t, r, err, credential.ChallengeSecretFields)
	if len(r.Save) != 0 || r.Save == nil {
		t.Error("not erased")
	}
}

func TestSSORoleGone(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	d := desc(nil)
	res, env := startSSO(t, p, f, d)
	f.mu.Lock()
	f.deviceStatus, f.roleMissing = "approved", true
	f.mu.Unlock()
	f.clock.Advance(5 * time.Second)
	if _, err := poll(t, p, env, d, res); !errors.Is(err, credential.ErrInvalidGrant) || !errors.Is(err, errSSORole) {
		t.Errorf("role missing at the end of the flow: %v", err)
	}
}

func TestSSODeviceOutcomes(t *testing.T) {
	for status, want := range map[string]error{"denied": credential.ErrInvalidGrant, "expired": credential.ErrChallengeExpired} {
		p, f := New(), newFakeAWS(t)
		d := desc(nil)
		res, env := startSSO(t, p, f, d)
		f.mu.Lock()
		f.deviceStatus = status
		f.mu.Unlock()
		f.clock.Advance(5 * time.Second)
		if _, err := poll(t, p, env, d, res); !errors.Is(err, want) {
			t.Errorf("%s: %v", status, err)
		}
	}
	// By the clock of the provider: no request.
	p, f := New(), newFakeAWS(t)
	d := desc(nil)
	res, env := startSSO(t, p, f, d)
	f.clock.Advance(11 * time.Minute)
	n := f.total()
	if _, err := poll(t, p, env, d, res); !errors.Is(err, credential.ErrChallengeExpired) || f.total() != n {
		t.Errorf("expiry: %v", err)
	}
}

func TestSSOSlowDown(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	d := desc(nil)
	res, env := startSSO(t, p, f, d)
	f.mu.Lock()
	f.deviceStatus = "slow"
	f.mu.Unlock()
	f.clock.Advance(5 * time.Second)
	w, err := poll(t, p, env, d, res)
	if err != nil || w.Challenge == nil || w.Challenge.IntervalSec != 10 {
		t.Fatalf("slow_down: %v %v", w, err)
	}
	res.Pending = w.Pending
	n := f.total()
	f.clock.Advance(5 * time.Second) // only 5 of the 10 seconds
	if w, err = poll(t, p, env, d, res); err != nil || w.Challenge == nil || f.total() != n {
		t.Errorf("polled again too early: %v %v", w, err)
	}
}

func TestSSOInput(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	env, d := f.env(), desc(nil)
	for name, tc := range map[string]func(a credential.Answer){
		"http start url":   func(a credential.Answer) { a["eksSsoStartUrl"] = "http://example.awsapps.com/start" },
		"start url user":   func(a credential.Answer) { a["eksSsoStartUrl"] = "https://u:p@example.awsapps.com/start" },
		"javascript url":   func(a credential.Answer) { a["eksSsoStartUrl"] = "javascript:alert(1)" },
		"empty start url":  func(a credential.Answer) { a["eksSsoStartUrl"] = "" },
		"bad sso region":   func(a credential.Answer) { a["eksSsoRegion"] = "eu-west-1.evil.example" },
		"bad account":      func(a credential.Answer) { a["eksAccountId"] = "1234" },
		"bad role":         func(a credential.Answer) { a["eksRoleName"] = "../Admin" },
		"empty role":       func(a credential.Answer) { a["eksRoleName"] = "" },
		"isolated sso reg": func(a credential.Answer) { a["eksSsoRegion"] = "us-iso-east-1" },
	} {
		a := credential.Answer{}
		for k, v := range ssoAnswer {
			a[k] = v
		}
		tc(a)
		res, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
		mustChallenge(t, res, err, credential.ChallengeSecretFields)
		res, err = step(t, p, env, d, res, nil, credential.Answer{"eksMethod": "sso"})
		mustChallenge(t, res, err, credential.ChallengeSecretFields)
		if _, err = step(t, p, env, d, res, nil, a); !errors.Is(err, credential.ErrInvalidAnswer) || !errors.Is(err, errSSOFields) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if f.total() != 0 {
		t.Errorf("%d requests for input that is not usable", f.total())
	}
}

// The link of the device flow is shown to the user, so only https is accepted on the web server.
func TestSSOVerificationAddressIsChecked(t *testing.T) {
	for _, uri := range []string{"javascript:alert(1)", "http://device.example.com/authorize", "file:///etc/passwd"} {
		p, f := New(), newFakeAWS(t)
		f.verifyURI = uri
		d := desc(nil)
		env := f.env()
		res, _ := p.Begin(t.Context(), env, credential.Request{Descriptor: d})
		res, _ = step(t, p, env, d, res, nil, credential.Answer{"eksMethod": "sso"})
		got, err := step(t, p, env, d, res, nil, ssoAnswer)
		if err == nil || got.Challenge != nil || !errors.Is(err, errSSORequest) {
			t.Errorf("%q: %v %v", uri, got, err)
		}
	}
}

func TestSSOWithRole(t *testing.T) {
	p, f := New(), newFakeAWS(t)
	d := desc(map[string]string{"role-arn": testRole})
	res, env := startSSO(t, p, f, d)
	f.mu.Lock()
	f.deviceStatus = "approved"
	f.mu.Unlock()
	f.clock.Advance(5 * time.Second)
	done, err := poll(t, p, env, d, res)
	tok := mustToken(t, done, err)
	if tok.Principal != testRole || len(f.roleSeen) != 1 {
		t.Errorf("principal %q roles %v", tok.Principal, f.roleSeen)
	}
	// The role is assumed with the credentials of the Identity Center role (the same temporary key here).
	if akid, _ := signedWith(t, tok); akid != testTempID {
		t.Errorf("signed with %q", akid)
	}
}
