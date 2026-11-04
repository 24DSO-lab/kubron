// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func TestWebBrowserCallback(t *testing.T) {
	idp := newFakeIdP(t, true)
	idp.deviceEnabled = false
	idp.issParam = true
	env := idp.env(false)
	d := idp.descriptor("authcode", map[string]string{
		"web.redirect-url": "https://kubron.example.test/auth/cluster/callback",
	})
	p := New()
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeBrowserCallback)
	if !ch.Poll || ch.IntervalSec != 2 {
		t.Fatalf("callback challenge should be polled: %+v", ch)
	}

	// The broker replaces the provider's provisional state with its newly generated challenge ID.
	const challengeID = "broker-generated-challenge-id"
	authURL, err := url.Parse(ch.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := authURL.Query()
	q.Set("state", challengeID)
	authURL.RawQuery = q.Encode()
	ch.ID = challengeID

	wait, err := p.Continue(context.Background(), env, credential.Request{
		Descriptor: d, Subject: subjectUser(), Pending: res.Pending, Challenge: ch,
	})
	if err != nil || wait.Challenge == nil || wait.Token != nil {
		t.Fatalf("poll before redirect: %+v, %v", wait, err)
	}

	redirect := idp.authorize(authURL.String(), "alice")
	if _, err := p.Continue(context.Background(), env, credential.Request{
		Descriptor: d, Subject: subjectUser(), Pending: res.Pending, Challenge: ch,
		Answer: credential.Answer{"redirectUrl": redirectWith(redirect, "iss", "https://evil.example.test")},
	}); !errors.Is(err, credential.ErrInvalidGrant) {
		t.Fatalf("RFC 9207 issuer mismatch: %v", err)
	}
	done, err := p.Continue(context.Background(), env, credential.Request{
		Descriptor: d, Subject: subjectUser(), Pending: res.Pending, Challenge: ch,
		Answer: credential.Answer{"redirectUrl": redirect},
	})
	tok := mustToken(t, done, err)
	if tok.Principal != "sub-alice" {
		t.Fatalf("principal = %q", tok.Principal)
	}
	if got := idp.forms[len(idp.forms)-1].Get("code"); got == "" {
		t.Fatal("callback did not exchange the authorization code")
	}
}

func TestWebBrowserCallbackRejectsForeignRedirectAndState(t *testing.T) {
	idp := newFakeIdP(t, true)
	idp.deviceEnabled = false
	env := idp.env(false)
	d := idp.descriptor("authcode", map[string]string{
		"web.redirect-url": "https://kubron.example.test/auth/cluster/callback",
	})
	p := New()
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	ch := mustChallenge(t, res, err, credential.ChallengeBrowserCallback)
	const challengeID = "broker-generated-challenge-id"
	authURL, _ := url.Parse(ch.URL)
	q := authURL.Query()
	q.Set("state", challengeID)
	authURL.RawQuery = q.Encode()
	ch.ID = challengeID

	for name, redirect := range map[string]string{
		"foreign host": "https://evil.example.test/auth/cluster/callback?code=x&state=" + challengeID,
		"foreign path": "https://kubron.example.test/other?code=x&state=" + challengeID,
		"foreign state": "https://kubron.example.test/auth/cluster/callback?code=x&state=wrong",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.Continue(context.Background(), env, credential.Request{
				Descriptor: d, Subject: subjectUser(), Pending: res.Pending, Challenge: ch,
				Answer: credential.Answer{"redirectUrl": redirect},
			})
			if err == nil || (!errors.Is(err, credential.ErrInvalidAnswer) && !errors.Is(err, credential.ErrAnswerRejected)) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
