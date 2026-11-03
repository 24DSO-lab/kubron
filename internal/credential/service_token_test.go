// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/credtest"
)

func (r *rig) signedIn() credential.Token {
	r.t.Helper()
	ch := r.login()
	res, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary))
	if err != nil || res.Token == nil {
		r.t.Fatalf("sign-in = %+v, %v", res, err)
	}
	return *res.Token
}

func (r *rig) savedKey() credential.Key {
	return credential.Key{ClusterID: subj.ClusterID, Scope: subj.Scope, OwnerID: subj.OwnerID}
}

func TestTokenWithoutSavedDataNeedsSignIn(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.Token(bg, subj, desc); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("err = %v", err)
	}
	// Token never creates a pending request: only Acquire does.
	if len(r.chal.Dump()) != 0 {
		t.Fatal("Token created a pending request")
	}
}

func TestTokenStatic(t *testing.T) {
	r := newRig(t)
	tok, err := r.svc.Token(bg, subj, credential.Descriptor{Kind: credential.KindStatic})
	if err != nil || tok.Value != "" {
		t.Fatalf("Token(static) = %+v, %v", tok, err)
	}
}

func TestTokenCacheAndMargin(t *testing.T) {
	r := newRig(t)
	first := r.signedIn() // expires in 5 minutes
	begins, _ := r.prov.Calls()

	got, err := r.svc.Token(bg, subj, desc)
	if err != nil || got.Value != first.Value {
		t.Fatalf("cached Token = %+v, %v", got, err)
	}
	if b, _ := r.prov.Calls(); b != begins {
		t.Fatal("the provider was called although the token is cached")
	}

	// 31 s before expiry: still served from the cache. 29 s before: refreshed.
	r.clk.Advance(5*time.Minute - 31*time.Second)
	if got, _ := r.svc.Token(bg, subj, desc); got.Value != first.Value {
		t.Fatal("a token with more than 30 s left must come from the cache")
	}
	r.clk.Advance(2 * time.Second)
	got, err = r.svc.Token(bg, subj, desc)
	if err != nil || got.Value == first.Value || got.Principal != "alice" {
		t.Fatalf("refreshed Token = %+v, %v", got, err)
	}
	// The rotated refresh token was saved, atomically with a new version.
	rec, err := r.store.Get(bg, r.savedKey())
	if err != nil || string(rec.Secret) != r.idp.LatestRefresh("alice") || rec.Version != 2 {
		t.Fatalf("saved = %q v%d, latest %q, %v", rec.Secret, rec.Version, r.idp.LatestRefresh("alice"), err)
	}
}

func TestTokenSingleFlight(t *testing.T) {
	r := newRig(t)
	r.signedIn()
	r.clk.Advance(10 * time.Minute) // the cached token is stale
	slow := r.prov.OnBegin
	r.prov.OnBegin = func(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
		time.Sleep(50 * time.Millisecond) // keep the flight open so that everybody joins it
		return slow(ctx, env, req)
	}
	before, _ := r.prov.Calls()
	var wg sync.WaitGroup
	values := make(chan string, 50)
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := r.svc.Token(bg, subj, desc)
			if err != nil {
				t.Error(err)
				return
			}
			values <- tok.Value
		}()
	}
	wg.Wait()
	close(values)
	first := ""
	for v := range values {
		if first == "" {
			first = v
		}
		if v != first {
			t.Fatalf("callers got different tokens: %q and %q", first, v)
		}
	}
	if after, _ := r.prov.Calls(); after-before != 1 {
		t.Fatalf("%d refreshes for 50 concurrent callers, want 1", after-before)
	}
	if r.prov.MaxConcurrent() != 1 {
		t.Fatalf("%d concurrent provider calls", r.prov.MaxConcurrent())
	}
}

func TestTokenCallerCanGiveUpWithoutBreakingOthers(t *testing.T) {
	r := newRig(t)
	r.signedIn()
	r.clk.Advance(10 * time.Minute)
	release := make(chan struct{})
	slow := r.prov.OnBegin
	r.prov.OnBegin = func(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
		<-release
		return slow(ctx, env, req)
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { _, err := r.svc.Token(ctx, subj, desc); done <- err }()
	time.Sleep(20 * time.Millisecond)
	patient := make(chan error, 1)
	go func() { _, err := r.svc.Token(bg, subj, desc); patient <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled caller got %v", err)
	}
	close(release)
	if err := <-patient; err != nil {
		t.Fatalf("the other caller was broken by the cancellation: %v", err)
	}
}

func TestInvalidGrantAsksForSignInAndErasesSavedData(t *testing.T) {
	r := newRig(t)
	r.signedIn()
	r.idp.RevokeRefresh(r.idp.LatestRefresh("alice")) // the user was disabled at the IdP
	r.clk.Advance(10 * time.Minute)
	svc := r.newServiceWithRecheck(r.prov, []time.Duration{time.Millisecond})
	if _, err := svc.Token(bg, subj, desc); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.store.Get(bg, r.savedKey()); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("the dead refresh token was kept: %v", err)
	}
	// The next interactive step starts from a fresh challenge.
	res, err := svc.Acquire(bg, subj, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
}

func TestInvalidGrantErrorFromProvider(t *testing.T) {
	r := newRig(t)
	prov := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, credential.ErrInvalidGrant
		}}
	if _, err := r.store.Put(bg, credential.Record{Meta: credential.Meta{Key: r.savedKey(), Kind: credential.KindOIDC}, Secret: []byte("rt")}, 0); err != nil {
		t.Fatal(err)
	}
	svc := r.newServiceWithRecheck(prov, []time.Duration{time.Millisecond})
	if _, err := svc.Token(bg, subj, desc); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestStoreFailureIsNotASignInRequest(t *testing.T) {
	r := newRig(t)
	boom := errors.New("disk is on fire")
	r.store = failingStore{CredentialStore: r.store, getErr: boom}
	svc := r.newService(r.prov)
	if _, err := svc.Token(bg, subj, desc); !errors.Is(err, boom) {
		t.Fatalf("err = %v: a broken store must not look like \"sign in again\"", err)
	}
}

type failingStore struct {
	credential.CredentialStore
	getErr, putErr error
}

func (f failingStore) Get(ctx context.Context, k credential.Key) (credential.Record, error) {
	if f.getErr != nil {
		return credential.Record{}, f.getErr
	}
	return f.CredentialStore.Get(ctx, k)
}

func (f failingStore) Put(ctx context.Context, r credential.Record, v int64) (credential.Meta, error) {
	if f.putErr != nil {
		return credential.Meta{}, f.putErr
	}
	return f.CredentialStore.Put(ctx, r, v)
}

func TestRotatedTokenThatCannotBeSavedStillServesTheRequest(t *testing.T) {
	r := newRig(t)
	r.signedIn()
	r.clk.Advance(10 * time.Minute)
	r.store = failingStore{CredentialStore: r.store, putErr: errors.New("read-only database")}
	svc := r.newService(r.prov)
	tok, err := svc.Token(bg, subj, desc)
	if err != nil || tok.Value == "" {
		t.Fatalf("Token = %+v, %v", tok, err)
	}
	if !strings.Contains(r.logs.String(), "could not be saved") {
		t.Fatal("the lost rotation is not reported")
	}
}

// Another replica rotates the refresh token between our read and our write. Its record stays, our (valid)
// token is returned, nothing is lost.
func TestConflictingWriteKeepsTheWinnersRecord(t *testing.T) {
	r := newRig(t)
	r.idp.StrictRotation = false
	r.signedIn()
	r.clk.Advance(10 * time.Minute)
	orig := r.prov.OnBegin
	r.prov.OnBegin = func(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
		res, err := orig(ctx, env, req)
		cur, _ := r.store.Get(bg, r.savedKey()) // the other replica saves first
		cur.Secret = []byte("rt-from-the-other-replica")
		if _, perr := r.store.Put(bg, cur, cur.Version); perr != nil {
			t.Error(perr)
		}
		return res, err
	}
	tok, err := r.svc.Token(bg, subj, desc)
	if err != nil || tok.Value == "" {
		t.Fatalf("Token = %+v, %v", tok, err)
	}
	rec, _ := r.store.Get(bg, r.savedKey())
	if string(rec.Secret) != "rt-from-the-other-replica" {
		t.Fatalf("the winner's record was overwritten: %q", rec.Secret)
	}
}

// The core of A.8: two replicas (two Services over one store) refresh the same subject again and again while
// the identity provider uses single-use refresh tokens. No sign-in request may appear, and the stored refresh
// token must be the newest one the IdP issued (no rotation is lost).
func TestTwoReplicasDoNotLoseRefreshTokenRotation(t *testing.T) {
	for _, strict := range []bool{true, false} {
		name := map[bool]string{true: "single-use refresh tokens", false: "reusable refresh tokens"}[strict]
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.idp.StrictRotation = strict
			r.idp.AccessTTL = time.Minute
			r.signedIn()
			replicas := []*credential.Service{
				r.newServiceWithRecheck(r.prov, []time.Duration{5 * time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond}),
				r.newServiceWithRecheck(r.prov, []time.Duration{5 * time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond}),
			}
			var failures atomic.Int32
			for round := 0; round < 25; round++ {
				r.clk.Advance(2 * time.Minute) // every cached token is stale in both replicas
				var wg sync.WaitGroup
				for i := range 6 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if _, err := replicas[i%2].Token(bg, subj, desc); err != nil {
							failures.Add(1)
							t.Errorf("round %d: %v", round, err)
						}
					}()
				}
				wg.Wait()
			}
			if failures.Load() != 0 {
				t.Fatalf("%d refreshes failed", failures.Load())
			}
			rec, err := r.store.Get(bg, r.savedKey())
			if err != nil {
				t.Fatal(err)
			}
			if strict {
				// With single-use tokens only the chain's head is valid: it must be what we stored.
				if string(rec.Secret) != r.idp.LatestRefresh("alice") {
					t.Fatalf("stored %q, the IdP's newest is %q: a rotation was lost", rec.Secret, r.idp.LatestRefresh("alice"))
				}
			}
			// Whatever is stored must still work.
			if _, err := r.idp.Refresh(string(rec.Secret)); err != nil {
				t.Fatalf("the stored refresh token is dead: %v", err)
			}
		})
	}
}

func (r *rig) newServiceWithRecheck(p credential.Provider, recheck []time.Duration) *credential.Service {
	reg := credential.NewRegistry(false)
	if err := reg.Register(p); err != nil {
		r.t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env,
		Now: r.clk.Now, RefreshRecheck: recheck})
	if err != nil {
		r.t.Fatal(err)
	}
	return svc
}

func TestTokenDoesNotLeakSecrets(t *testing.T) {
	r := newRig(t)
	r.signedIn()
	r.clk.Advance(10 * time.Minute)
	tok, err := r.svc.Token(bg, subj, desc)
	if err != nil {
		t.Fatal(err)
	}
	rt := r.idp.LatestRefresh("alice")
	for _, sec := range []string{tok.Value, rt, csCanary, pwCanary} {
		if strings.Contains(r.logs.String(), sec) {
			t.Errorf("%q found in the logs", sec)
		}
	}
	if s := tok.String() + " " + (credential.Result{Token: &tok}).String(); strings.Contains(s, tok.Value) {
		t.Errorf("the token prints itself: %s", s)
	}
	// A leaky provider error on the refresh path is scrubbed too.
	leaky := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			return credential.Result{}, credtest.LeakyError(string(req.Saved) + " " + req.Descriptor.Params["client-secret"])
		}}
	r.clk.Advance(10 * time.Minute)
	svc := r.newService(leaky)
	_, err = svc.Token(bg, subj, desc)
	if err == nil || strings.Contains(err.Error(), csCanary) || strings.Contains(err.Error(), r.idp.LatestRefresh("alice")) {
		t.Fatalf("leak in the error: %v", err)
	}
}

func TestRevoke(t *testing.T) {
	r := newRig(t)
	pending := r.login() // a pending request of u1 on c1 (created before the sign-in, which would otherwise be silent)
	r.signedIn()
	other := credential.Subject{ClusterID: "c2", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "s1"}
	otherUser := credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u2", SessionID: "s2"}
	r.idp.AddUser("bob", "bob-pw")
	for _, s := range []credential.Subject{other, otherUser} {
		res, _ := r.svc.Acquire(bg, s, desc)
		user, pw := "alice", pwCanary
		if s.OwnerID == "u2" {
			user, pw = "bob", "bob-pw"
		}
		if _, err := r.svc.Answer(bg, s, res.Challenge.ID, answer(user, pw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.svc.Revoke(bg, credential.RevokeFilter{ClusterID: "c1", OwnerID: "u1"}); err != nil {
		t.Fatal(err)
	}
	// u1/c1: the saved data, the cached token and the pending request are gone.
	if _, err := r.svc.Token(bg, subj, desc); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("revoked subject still has a token: %v", err)
	}
	if _, err := r.svc.Answer(bg, subj, pending.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("the pending request of a revoked subject survived: %v", err)
	}
	// Other cluster and other user are untouched.
	if _, err := r.svc.Token(bg, other, desc); err != nil {
		t.Fatalf("other cluster: %v", err)
	}
	if _, err := r.svc.Token(bg, otherUser, desc); err != nil {
		t.Fatalf("other user: %v", err)
	}
	// Revoking a whole cluster.
	if err := r.svc.Revoke(bg, credential.RevokeFilter{ClusterID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Token(bg, otherUser, desc); !errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("cluster revoke missed a user: %v", err)
	}
}

func TestList(t *testing.T) {
	r := newRig(t)
	r.signedIn()
	got, err := r.svc.List(bg, "u1")
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %+v, %v", got, err)
	}
	c := got[0]
	if c.ClusterID != "c1" || c.Kind != credential.KindOIDC || c.Scope != credential.ScopeUser || c.Principal != "alice" ||
		c.ExpiresAt == nil || !c.ExpiresAt.Equal(r.clk.Now().Add(5*time.Minute)) {
		t.Fatalf("info = %+v", c)
	}
	if none, err := r.svc.List(bg, "u2"); err != nil || len(none) != 0 {
		t.Fatalf("List(u2) = %+v, %v", none, err)
	}
}
