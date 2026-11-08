// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// legacyTokens signs in once to get a pair of tokens like the ones kubectl leaves in a kubeconfig.
func legacyTokens(t *testing.T, idp *fakeIdP) (idToken, refresh string) {
	t.Helper()
	res, err := signInWithPassword(t, idp, idp.env(true), nil)
	mustToken(t, res, err)
	return idp.lastIDToken, idp.lastRefresh
}

func legacyDescriptor(idp *fakeIdP, id, rt string) credential.Descriptor {
	p := map[string]string{"issuer-url": idp.issuer(), "client-id": testClient, "grant": "legacy-refresh"}
	if id != "" {
		p["id-token"] = id
	}
	if rt != "" {
		p["refresh-token"] = rt
	}
	return credential.Descriptor{Kind: credential.KindOIDC, Params: p}
}

func TestLegacyRefresh(t *testing.T) {
	ctx := context.Background()

	t.Run("a valid ID token is used as it is", func(t *testing.T) {
		idp := newFakeIdP(t, false)
		id, rt := legacyTokens(t, idp)
		before := idp.hit("/token")
		d := legacyDescriptor(idp, id, rt)
		res, err := New().Begin(ctx, idp.env(true), credential.Request{Descriptor: d, Subject: subjectUser()})
		tok := mustToken(t, res, err)
		if tok.Value != id || tok.Principal != "sub-alice" || idp.hit("/token") != before {
			t.Fatalf("token %v, token requests %d", tok.Value == id, idp.hit("/token")-before)
		}
		if sv, err := parseSaved(res.Save); err != nil || sv.RefreshToken != rt {
			t.Fatalf("the refresh token of the kubeconfig is kept for later: %s %v", res.Save, err)
		}
	})

	t.Run("an expired ID token is refreshed and the kubeconfig stays as it was", func(t *testing.T) {
		idp := newFakeIdP(t, false)
		id, rt := legacyTokens(t, idp)
		idp.clock.Advance(6 * time.Minute)
		d := legacyDescriptor(idp, id, rt)
		res, err := New().Begin(ctx, idp.env(true), credential.Request{Descriptor: d, Subject: subjectUser()})
		tok := mustToken(t, res, err)
		if tok.Value == id || tok.Value != idp.lastIDToken {
			t.Fatal("a new ID token was expected")
		}
		sv, err := parseSaved(res.Save)
		if err != nil || sv.RefreshToken == rt || sv.RefreshToken != idp.lastRefresh {
			t.Fatalf("the rotated refresh token goes to Save: %s %v", res.Save, err)
		}
		if d.Params["refresh-token"] != rt || d.Params["id-token"] != id {
			t.Fatal("the descriptor (the kubeconfig) must not be changed")
		}
		// the next Begin takes what is saved, not the old token of the kubeconfig
		res2, err := New().Begin(ctx, idp.env(true), credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
		mustToken(t, res2, err)
	})

	t.Run("a forged ID token is not used", func(t *testing.T) {
		idp := newFakeIdP(t, false)
		id, rt := legacyTokens(t, idp)
		forged := id[:len(id)-6] + "AAAAAA"
		res, err := New().Begin(ctx, idp.env(true), credential.Request{Descriptor: legacyDescriptor(idp, forged, rt), Subject: subjectUser()})
		if tok := mustToken(t, res, err); tok.Value == forged {
			t.Fatal("the forged token was returned")
		}
	})

	t.Run("only a refresh token", func(t *testing.T) {
		idp := newFakeIdP(t, false)
		_, rt := legacyTokens(t, idp)
		res, err := New().Begin(ctx, idp.env(true), credential.Request{Descriptor: legacyDescriptor(idp, "", rt), Subject: subjectUser()})
		mustToken(t, res, err)
	})

	t.Run("a dead refresh token leads to the interactive flow", func(t *testing.T) {
		for _, desktop := range []bool{true, false} {
			idp := newFakeIdP(t, !desktop)
			d := legacyDescriptor(idp, "", "dead-token")
			d.Params["listen-address"] = "127.0.0.1:0"
			p := New()
			res, err := p.Begin(ctx, idp.env(desktop), credential.Request{Descriptor: d, Subject: subjectUser()})
			want := credential.ChallengeDeviceCode
			if desktop {
				want = credential.ChallengeBrowserLoopback
			}
			mustChallenge(t, res, err, want)
			p.Close()
		}
	})

	t.Run("nothing in the kubeconfig", func(t *testing.T) {
		idp := newFakeIdP(t, true)
		res, err := New().Begin(ctx, idp.env(false), credential.Request{Descriptor: legacyDescriptor(idp, "", ""), Subject: subjectUser()})
		mustChallenge(t, res, err, credential.ChallengeDeviceCode)
	})

	t.Run("an unreachable provider is not mistaken for a dead token", func(t *testing.T) {
		idp := newFakeIdP(t, false)
		_, rt := legacyTokens(t, idp)
		d := legacyDescriptor(idp, "", rt)
		idp.srv.Close()
		_, err := New().Begin(ctx, idp.env(true), credential.Request{Descriptor: d, Subject: subjectUser()})
		if err == nil || errors.Is(err, credential.ErrInvalidGrant) {
			t.Fatalf("got %v", err)
		}
	})
}
