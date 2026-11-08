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

func devicePoll(p *Provider, env credential.Env, d credential.Descriptor, res credential.Result) (credential.Result, error) {
	return p.Continue(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Challenge: *res.Challenge, Pending: res.Pending})
}

func beginDevice(t *testing.T, p *Provider, idp *fakeIdP, grant string, desktop bool) (credential.Result, credential.Env, credential.Descriptor, error) {
	env, d := idp.env(desktop), idp.descriptor(grant, nil)
	res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	return res, env, d, err
}

func TestDeviceFlowOnTheWeb(t *testing.T) {
	for _, grant := range []string{"authcode", "device-code"} {
		idp := newFakeIdP(t, true)
		p := New()
		res, env, d, err := beginDevice(t, p, idp, grant, false)
		ch := mustChallenge(t, res, err, credential.ChallengeDeviceCode)
		if ch.UserCode != "ABCD-EFGH" || !ch.Poll || ch.IntervalSec != 5 || ch.URL != idp.issuer()+"/device-login?user_code=ABCD-EFGH" {
			t.Fatalf("%s: %+v", grant, ch)
		}
		if want := idp.clock.Now().Add(600 * time.Second); !ch.ExpiresAt.Equal(want) {
			t.Fatalf("ExpiresAt %v, want %v", ch.ExpiresAt, want)
		}
		if f := idp.forms[0]; f.Get("scope") != "openid" || f.Get("client_id") != testClient {
			t.Fatalf("device request %v", f)
		}

		// an early poll is answered without asking the provider
		w, err := devicePoll(p, env, d, res)
		if err != nil || w.Challenge == nil || w.Token != nil || idp.hit("/token") != 0 {
			t.Fatalf("early poll: %+v %v (token requests %d)", w, err, idp.hit("/token"))
		}
		// in time: authorization_pending
		idp.clock.Advance(5 * time.Second)
		w, err = devicePoll(p, env, d, res)
		if err != nil || w.Challenge == nil || idp.hit("/token") != 1 {
			t.Fatalf("pending: %+v %v", w, err)
		}
		res.Pending = w.Pending
		// the user approves
		idp.approveDevice("alice", false)
		idp.clock.Advance(5 * time.Second)
		done, err := devicePoll(p, env, d, res)
		tok := mustToken(t, done, err)
		if tok.Principal != "sub-alice" {
			t.Fatalf("principal %q", tok.Principal)
		}
		last := idp.forms[len(idp.forms)-1]
		if last.Get("grant_type") != deviceGrantType || last.Get("device_code") == "" {
			t.Fatalf("token request %v", last)
		}
		if sv, err := parseSaved(done.Save); err != nil || sv.RefreshToken == "" {
			t.Fatalf("saved %s %v", done.Save, err)
		}
	}
}

func TestDeviceSlowDown(t *testing.T) {
	idp := newFakeIdP(t, true)
	idp.deviceEvery = 10 * time.Second // the provider wants 10 seconds but advertises 5
	p := New()
	res, env, d, err := beginDevice(t, p, idp, "device-code", false)
	mustChallenge(t, res, err, credential.ChallengeDeviceCode)

	sawSlowDown, polls := false, 0
	for i := 0; i < 12; i++ {
		idp.clock.Advance(time.Duration(res.Challenge.IntervalSec) * time.Second)
		before := idp.hit("/token")
		w, err := devicePoll(p, env, d, res)
		if err != nil {
			t.Fatal(err)
		}
		polls += idp.hit("/token") - before
		if w.Token != nil {
			if !sawSlowDown {
				t.Fatal("expected a slow_down on the way")
			}
			if polls > 6 {
				t.Fatalf("the provider was asked %d times", polls)
			}
			return
		}
		if w.Challenge.IntervalSec > res.Challenge.IntervalSec {
			sawSlowDown = true
			idp.approveDevice("alice", false)
		}
		res = credential.Result{Challenge: w.Challenge, Pending: w.Pending}
	}
	t.Fatal("the sign-in never completed")
}

func TestDeviceExpiry(t *testing.T) {
	// by the clock of the provider process: no request is made
	idp := newFakeIdP(t, true)
	p := New()
	res, env, d, err := beginDevice(t, p, idp, "device-code", false)
	mustChallenge(t, res, err, credential.ChallengeDeviceCode)
	idp.clock.Advance(11 * time.Minute)
	if _, err := devicePoll(p, env, d, res); !errors.Is(err, credential.ErrChallengeExpired) || idp.hit("/token") != 0 {
		t.Fatalf("got %v (%d requests)", err, idp.hit("/token"))
	}

	// by the answer of the provider (it forgot the code earlier than it said)
	idp = newFakeIdP(t, true)
	idp.deviceTTL = time.Minute
	res, env, d, err = beginDevice(t, p, idp, "device-code", false)
	mustChallenge(t, res, err, credential.ChallengeDeviceCode)
	idp.clock.Advance(61 * time.Second)
	if _, err := devicePoll(p, env, d, res); !errors.Is(err, credential.ErrChallengeExpired) || idp.hit("/token") != 1 {
		t.Fatalf("expired_token: got %v (%d requests)", err, idp.hit("/token"))
	}
}

func TestDeviceDenied(t *testing.T) {
	idp := newFakeIdP(t, true)
	p := New()
	res, env, d, err := beginDevice(t, p, idp, "device-code", false)
	mustChallenge(t, res, err, credential.ChallengeDeviceCode)
	idp.approveDevice("", true)
	idp.clock.Advance(5 * time.Second)
	_, err = devicePoll(p, env, d, res)
	if !errors.Is(err, errDenied) || !errors.Is(err, credential.ErrInvalidGrant) {
		t.Fatalf("got %v", err)
	}
}

func TestDeviceBadPending(t *testing.T) {
	idp := newFakeIdP(t, true)
	p := New()
	res, env, d, err := beginDevice(t, p, idp, "device-code", false)
	mustChallenge(t, res, err, credential.ChallengeDeviceCode)
	for _, bad := range [][]byte{nil, []byte("x"), []byte(`{"v":1,"flow":"paste"}`), []byte(`{"v":1,"flow":"device"}`)} {
		res.Pending = bad
		if _, err := devicePoll(p, env, d, res); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestDeviceVerificationAddressIsChecked(t *testing.T) {
	for _, uri := range []string{"javascript:alert(1)", "http://idp.example.com/device"} {
		idp := newFakeIdP(t, true)
		idp.deviceURI = uri
		_, _, _, err := beginDevice(t, New(), idp, "device-code", false)
		if !errors.Is(err, errTokenResponse) {
			t.Errorf("%q: got %v", uri, err)
		}
	}
}

// What a user gets depends on the mode and on what the provider offers.
func TestOrderOfFlows(t *testing.T) {
	type setup func(*fakeIdP)
	none := func(*fakeIdP) {}
	absent := func(f *fakeIdP) { f.deviceEnabled = false }
	refused := func(f *fakeIdP) { f.deviceRefuse = true }
	for name, tc := range map[string]struct {
		grant    string
		desktop  bool
		idp      setup
		want     credential.ChallengeKind
		deviceUp int // requests to the device endpoint
	}{
		"web authcode, device offered":       {"authcode", false, none, credential.ChallengeDeviceCode, 1},
		"web authcode, no device endpoint":   {"authcode", false, absent, credential.ChallengePasteRedirect, 0},
		"web authcode, unauthorized_client":  {"authcode", false, refused, credential.ChallengePasteRedirect, 1},
		"web device-code, device offered":    {"device-code", false, none, credential.ChallengeDeviceCode, 1},
		"web device-code, falls back":        {"device-code", false, refused, credential.ChallengePasteRedirect, 1},
		"web keyboard never uses the device": {"authcode-keyboard", false, none, credential.ChallengePasteRedirect, 0},
		"desktop authcode: listener first":   {"authcode", true, none, credential.ChallengeBrowserLoopback, 0},
		"desktop device-code, offered":       {"device-code", true, none, credential.ChallengeDeviceCode, 1},
		"desktop device-code, falls back":    {"device-code", true, refused, credential.ChallengeBrowserLoopback, 1},
	} {
		idp := newFakeIdP(t, !tc.desktop)
		tc.idp(idp)
		p := New()
		env, d := idp.env(tc.desktop), idp.descriptor(tc.grant, map[string]string{"listen-address": "127.0.0.1:0"})
		res, err := p.Begin(context.Background(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
		mustChallenge(t, res, err, tc.want)
		if got := idp.hit("/device"); got != tc.deviceUp {
			t.Errorf("%s: %d requests to the device endpoint, want %d", name, got, tc.deviceUp)
		}
		p.Close()
	}
}
