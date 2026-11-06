// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"errors"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

func TestStateRemembersAndForgetsChallenges(t *testing.T) {
	s := NewState()
	if _, ok := s.Lookup("c1"); ok {
		t.Fatal("an unknown challenge must not be found")
	}
	want := Pending{Cluster: "k1", IdP: "idp:example.com", Sub: credential.Subject{ClusterID: "k1"}}
	s.Remember("c1", want)
	if got, ok := s.Lookup("c1"); !ok || got != want {
		t.Fatalf("Lookup = %+v %v", got, ok)
	}
	s.Forget("c1")
	if _, ok := s.Lookup("c1"); ok {
		t.Fatal("a forgotten challenge must be gone")
	}
}

func TestStateSessionsDoNotShareChallenges(t *testing.T) {
	a, b := NewState(), NewState()
	a.Remember("c1", Pending{Cluster: "k1"})
	if _, ok := b.Lookup("c1"); ok {
		t.Fatal("a challenge belongs to the session that started it")
	}
}

func TestAuthRequiredIsSentOncePerWindowAndCluster(t *testing.T) {
	s := NewState()
	now := time.Unix(1_700_000_000, 0)
	if !s.AuthRequiredDue("k1", now) {
		t.Fatal("the first event must be sent")
	}
	if s.AuthRequiredDue("k1", now.Add(AuthRequiredEvery-time.Millisecond)) {
		t.Fatal("a repeat inside the window must be suppressed")
	}
	if !s.AuthRequiredDue("k2", now) {
		t.Fatal("another cluster has its own window")
	}
	if !s.AuthRequiredDue("k1", now.Add(AuthRequiredEvery)) {
		t.Fatal("after the window the event is due again")
	}
}

func TestMarkSeenNeverGoesBack(t *testing.T) {
	s := NewState()
	s.MarkSeen(5)
	s.MarkSeen(3)
	if got := s.Seen(); got != 5 {
		t.Fatalf("Seen = %d", got)
	}
	s.MarkSeen(9)
	if got := s.Seen(); got != 9 {
		t.Fatalf("Seen = %d", got)
	}
}

type bucket struct {
	allow bool
	wait  time.Duration
	keys  []string
}

func (b *bucket) Allow(key string) (bool, time.Duration) {
	b.keys = append(b.keys, key)
	return b.allow, b.wait
}

func TestLimiterWithoutLimitsAllowsEverything(t *testing.T) {
	called := false
	if err := (Limiter{}).Take(func() string { called = true; return "u1" }, "idp:x"); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("the user must not be looked up when there is no per-user limit")
	}
}

func TestLimiterUsesUserBucketBeforeIdPBucket(t *testing.T) {
	user, idp := &bucket{allow: false, wait: 1500 * time.Millisecond}, &bucket{allow: true}
	err := Limiter{User: user, IdP: idp}.Take(func() string { return "u1" }, "idp:x")
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("got %v", err)
	}
	if rl.RetryAfterSeconds() != 2 {
		t.Fatalf("RetryAfterSeconds = %d, want the wait rounded up to 2", rl.RetryAfterSeconds())
	}
	if len(idp.keys) != 0 {
		t.Fatal("a refused user must not spend the allowance of the provider")
	}
	if user.keys[0] != "answer:u1" {
		t.Fatalf("user key %q", user.keys[0])
	}
}

func TestLimiterIdPBucketAndAnonymousUser(t *testing.T) {
	user, idp := &bucket{allow: true}, &bucket{allow: false, wait: time.Second}
	err := Limiter{User: user, IdP: idp}.Take(func() string { return "" }, "idp:x")
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfterSeconds() != 1 {
		t.Fatalf("got %v", err)
	}
	if len(user.keys) != 0 {
		t.Fatal("without a user id the per-user bucket is skipped")
	}
	if idp.keys[0] != "answer:idp:x" {
		t.Fatalf("idp key %q", idp.keys[0])
	}
}

func TestRetryAfterIsAtLeastOneSecond(t *testing.T) {
	if got := (&RateLimitedError{}).RetryAfterSeconds(); got != 1 {
		t.Fatalf("got %d", got)
	}
}

func TestIDPKey(t *testing.T) {
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"issuer-url": "https://Login.Example.com/realm"}}
	if got := IDPKey(d, "k1"); got != "idp:login.example.com" {
		t.Fatalf("got %q", got)
	}
	cloud := credential.Descriptor{Kind: credential.KindStatic}
	if got := IDPKey(cloud, "k1"); got != "kind:"+string(credential.KindStatic)+":k1" {
		t.Fatalf("got %q", got)
	}
}

func TestWithWebOIDCParamsDropsClusterSuppliedOverrides(t *testing.T) {
	base := credential.Descriptor{Params: map[string]string{"issuer-url": "https://i", "web.client-id": "evil", "web.redirect-url": "https://evil"}}

	got := WithWebOIDCParams(base, "", "")
	if _, ok := got.Params["web.client-id"]; ok {
		t.Fatal("an override coming from cluster data must be dropped")
	}
	if got.Params["issuer-url"] != "https://i" {
		t.Fatal("other params must be kept")
	}
	if base.Params["web.client-id"] != "evil" {
		t.Fatal("the input descriptor must not be modified")
	}

	got = WithWebOIDCParams(base, "client", "https://app/cb")
	if got.Params["web.client-id"] != "client" || got.Params["web.redirect-url"] != "https://app/cb" {
		t.Fatalf("got %+v", got.Params)
	}
	got = WithWebOIDCParams(base, "client", "")
	if _, ok := got.Params["web.client-id"]; ok {
		t.Fatal("the callback needs both the client and the redirect URL")
	}
}

func TestSharedAccountErrorIsAChallengeRequired(t *testing.T) {
	if !errors.Is(SharedAccountNotSignedIn{}, credential.ErrChallengeRequired) {
		t.Fatal("must be recognised as ErrChallengeRequired")
	}
	if errors.Is(SharedAccountNotSignedIn{}, credential.ErrChallengeExpired) {
		t.Fatal("must not match unrelated errors")
	}
}

func TestPrincipalOf(t *testing.T) {
	if got := PrincipalOf(credential.Result{}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := PrincipalOf(credential.Result{Token: &credential.Token{Principal: "ann"}}); got != "ann" {
		t.Fatalf("got %q", got)
	}
}
