// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/credtest"
	"github.com/24DSO-labs/kubron/internal/credential/filestore"
	"github.com/24DSO-labs/kubron/internal/secretbox"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

const (
	pwCanary = "CANARY-password-9a1f"
	csCanary = "CANARY-client-secret-7c2e"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	t      *testing.T
	clk    *clock
	idp    *credtest.FakeIdP
	prov   *credtest.FakeProvider
	store  credential.CredentialStore
	chal   *credential.MemoryChallengeStore
	env    *secrets.Envelope
	logs   *bytes.Buffer
	dir    string
	svc    *credential.Service
	storeP string
}

func newKeyring(t *testing.T) *secretbox.Keyring {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	kr, err := secretbox.NewKeyring(k)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, clk: newClock(), chal: credential.NewMemoryChallengeStore(), logs: &bytes.Buffer{}, dir: t.TempDir()}
	r.idp = credtest.NewFakeIdP(r.clk.Now)
	r.idp.AddUser("alice", pwCanary)
	r.prov = credtest.NewPasswordProvider(credential.KindOIDC, r.idp)
	env, err := secrets.NewEnvelope(newKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	r.env = env
	r.storeP = filepath.Join(r.dir, "credentials.json")
	r.store = r.openStore()
	r.svc = r.newService(r.prov)
	return r
}

func (r *rig) openStore() credential.CredentialStore {
	st, err := filestore.New(r.storeP, credential.NewSealer(r.env))
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *rig) newService(p credential.Provider) *credential.Service {
	reg := credential.NewRegistry(false)
	if err := reg.Register(p); err != nil {
		r.t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env,
		Now: r.clk.Now, Logger: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		r.t.Fatal(err)
	}
	return svc
}

var (
	subj = credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "s1"}
	desc = credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"grant": "password", "client-secret": csCanary}}
	bg   = context.Background()
)

func (r *rig) login() credential.Challenge {
	r.t.Helper()
	res, err := r.svc.Acquire(bg, subj, desc)
	if err != nil || res.Challenge == nil {
		r.t.Fatalf("Acquire = %+v, %v", res, err)
	}
	return *res.Challenge
}

func answer(user, pw string) credential.Answer {
	return credential.Answer{"username": user, "password": pw}
}

func TestMemoryChallengeStoreContract(t *testing.T) {
	credtest.ChallengeStoreContract(t, func(t *testing.T) credtest.ChallengeHarness {
		st := credential.NewMemoryChallengeStore()
		return credtest.ChallengeHarness{Store: st, Second: func(*testing.T) credential.ChallengeStore { return st }}
	})
}

func TestNewServiceRequiresEverything(t *testing.T) {
	if _, err := credential.NewService(credential.Config{}); !errors.Is(err, credential.ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

func TestAcquireStaticNeedsNothing(t *testing.T) {
	r := newRig(t)
	res, err := r.svc.Acquire(bg, subj, credential.Descriptor{Kind: credential.KindStatic})
	if err != nil || res.Token != nil || res.Challenge != nil {
		t.Fatalf("Acquire(static) = %+v, %v", res, err)
	}
	if b, _ := r.prov.Calls(); b != 0 {
		t.Fatal("a provider was called for a static descriptor")
	}
}

func TestAcquireValidatesSubject(t *testing.T) {
	r := newRig(t)
	for _, s := range []credential.Subject{{}, {ClusterID: "c1", Scope: credential.ScopeUser}, {ClusterID: "a/b", Scope: credential.ScopeUser, OwnerID: "u"}} {
		if _, err := r.svc.Acquire(bg, s, desc); !errors.Is(err, credential.ErrInvalidKey) {
			t.Fatalf("Acquire(%+v) = %v", s, err)
		}
	}
	if _, err := r.svc.Acquire(bg, subj, credential.Descriptor{}); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("empty descriptor: %v", err)
	}
	if _, err := r.svc.Acquire(bg, subj, credential.Descriptor{Kind: credential.KindGKE}); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("no provider: %v", err)
	}
}

func TestChallengeLifecycle(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	if len(ch.ID) != 22 || ch.Kind != credential.ChallengeCredentials || !ch.ExpiresAt.Equal(r.clk.Now().Add(credential.ChallengeTTL)) {
		t.Fatalf("challenge = %+v", ch)
	}
	res, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary))
	if err != nil || res.Token == nil || res.Token.Principal != "alice" || res.Token.Value == "" {
		t.Fatalf("Answer = %+v, %v", res, err)
	}
	if res.Save != nil || res.Pending != nil {
		t.Fatal("internal result fields leaked to the caller")
	}
	// The refresh token is saved, the password is not.
	rec, err := r.store.Get(bg, credential.Key{ClusterID: subj.ClusterID, Scope: subj.Scope, OwnerID: subj.OwnerID})
	if err != nil || !strings.HasPrefix(string(rec.Secret), "rt-") || rec.Kind != credential.KindOIDC || rec.Principal != "alice" {
		t.Fatalf("saved = %+v, %v", rec, err)
	}
	// One-time completion: a replay is rejected.
	if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("replay = %v", err)
	}
	// The next Acquire uses the saved data silently.
	r.clk.Advance(time.Minute)
	res, err = r.svc.Acquire(bg, subj, desc)
	if err != nil || res.Token == nil || res.Challenge != nil {
		t.Fatalf("silent Acquire = %+v, %v", res, err)
	}
}

func TestIDsAreUnpredictable(t *testing.T) {
	r := newRig(t)
	seen := map[string]bool{}
	for range 50 {
		id := r.login().ID
		if seen[id] {
			t.Fatal("duplicate challenge id")
		}
		seen[id] = true
	}
}

func TestFiveWrongAnswersDestroyTheRequest(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	for i := 1; i <= credential.MaxAnswerAttempts; i++ {
		if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", "wrong")); !errors.Is(err, credential.ErrAnswerRejected) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Even the right answer no longer works.
	if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("after the limit: %v", err)
	}
}

func TestFourWrongThenRightStillWorks(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	for range credential.MaxAnswerAttempts - 1 {
		if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", "wrong")); !errors.Is(err, credential.ErrAnswerRejected) {
			t.Fatal(err)
		}
	}
	if res, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); err != nil || res.Token == nil {
		t.Fatalf("right answer = %+v, %v", res, err)
	}
}

func TestChallengeIsBoundToSessionAndUser(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	foreign := []credential.Subject{
		{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "other-session"},
		{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u2", SessionID: "s1"},
		{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: ""},
		{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "", SessionID: "s1"},
		{ClusterID: "other", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "s1"},
		{ClusterID: "c1", Scope: credential.ScopeTeam, OwnerID: "u1", SessionID: "s1"},
	}
	for _, f := range foreign {
		if _, err := r.svc.Answer(bg, f, ch.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("Answer as %+v = %v, want ErrChallengeExpired", f, err)
		}
		if _, err := r.svc.Poll(bg, f, ch.ID); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("Poll as %+v = %v", f, err)
		}
		if err := r.svc.Cancel(bg, f, ch.ID); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Errorf("Cancel as %+v = %v", f, err)
		}
	}
	// None of that consumed an attempt or destroyed the request; the id alone (no cluster/scope) is enough for the owner.
	owner := credential.Subject{OwnerID: "u1", SessionID: "s1"}
	if res, err := r.svc.Answer(bg, owner, ch.ID, answer("alice", pwCanary)); err != nil || res.Token == nil {
		t.Fatalf("the owner was locked out: %+v, %v", res, err)
	}
	// An unknown id is indistinguishable from a foreign one.
	if _, err := r.svc.Answer(bg, subj, "no-such-id", answer("a", "b")); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestChallengeExpires(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	r.clk.Advance(credential.ChallengeTTL - time.Second)
	if _, err := r.svc.Poll(bg, subj, ch.ID); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	r.clk.Advance(2 * time.Second)
	if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("after expiry: %v", err)
	}
	ch2 := r.login()
	r.clk.Advance(credential.ChallengeTTL + time.Second)
	if n, err := r.svc.Sweep(bg); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	_ = ch2
}

func TestPollOfAnAnswerChallengeDoesNotBurnAttempts(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	for range 3 * credential.MaxAnswerAttempts {
		res, err := r.svc.Poll(bg, subj, ch.ID)
		if err != nil || res.Challenge == nil || res.Challenge.ID != ch.ID {
			t.Fatalf("Poll = %+v, %v", res, err)
		}
	}
	if _, cont := r.prov.Calls(); cont != 0 {
		t.Fatalf("the provider was called %d times by polls", cont)
	}
	if res, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); err != nil || res.Token == nil {
		t.Fatalf("Answer after polls = %+v, %v", res, err)
	}
}

func TestCancel(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	if err := r.svc.Cancel(bg, subj, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("after Cancel: %v", err)
	}
	if err := r.svc.Cancel(bg, subj, ch.ID); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
}

// Two replicas answer the same request at once: exactly one completes it.
func TestOnlyOneCompletionAcrossReplicas(t *testing.T) {
	r := newRig(t)
	other := r.newService(r.prov) // second replica: same stores, same provider
	ch := r.login()
	var ok, expired atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := r.svc
			if i%2 == 1 {
				svc = other
			}
			res, err := svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary))
			switch {
			case err == nil && res.Token != nil:
				ok.Add(1)
			case errors.Is(err, credential.ErrChallengeExpired):
				expired.Add(1)
			default:
				t.Errorf("unexpected: %+v, %v", res, err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || expired.Load() != 19 {
		t.Fatalf("completions = %d, rejected = %d", ok.Load(), expired.Load())
	}
	if _, c := r.prov.Calls(); c != 1 {
		t.Fatalf("Continue ran %d times, want 1", c)
	}
}

func TestCrashedWorkerClaimTimesOut(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	rec, err := r.chal.Get(bg, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.Status, rec.LockedUntil = credential.ChallengeBusy, r.clk.Now().Add(30*time.Second)
	if _, err := r.chal.Swap(bg, rec, rec.Version); err != nil { // a worker claimed it and died
		t.Fatal(err)
	}
	if _, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("while claimed: %v", err)
	}
	r.clk.Advance(31 * time.Second)
	if res, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary)); err != nil || res.Token == nil {
		t.Fatalf("after the claim timed out: %+v, %v", res, err)
	}
}

func TestDeviceCodePolling(t *testing.T) {
	r := newRig(t)
	prov := credtest.NewDeviceCodeProvider(credential.KindOIDC, r.idp)
	svc := r.newService(prov)
	res, err := svc.Acquire(bg, subj, desc)
	if err != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeDeviceCode || !res.Challenge.Poll {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	id := res.Challenge.ID
	for range 3 {
		res, err = svc.Poll(bg, subj, id)
		if err != nil || res.Challenge == nil || res.Challenge.ID != id || res.Token != nil {
			t.Fatalf("pending Poll = %+v, %v", res, err)
		}
	}
	// A poll by somebody else neither completes nor reveals anything.
	if _, err := svc.Poll(bg, credential.Subject{OwnerID: "u2", SessionID: "s1"}, id); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("foreign poll: %v", err)
	}
	r.idp.ApproveDevice(string(prov.LastRequest().Pending), "alice")
	res, err = svc.Poll(bg, subj, id)
	if err != nil || res.Token == nil || res.Token.Principal != "alice" {
		t.Fatalf("Poll after approval = %+v, %v", res, err)
	}
	if _, err := svc.Poll(bg, subj, id); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("poll after completion: %v", err)
	}
}

func TestProviderErrorsAreScrubbed(t *testing.T) {
	r := newRig(t)
	leaky := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials}}, nil
		},
		OnContinue: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			return credential.Result{}, credtest.LeakyError(req.Answer["password"] + " " + req.Descriptor.Params["client-secret"])
		}}
	svc := r.newService(leaky)
	res, _ := svc.Acquire(bg, subj, desc)
	_, err := svc.Answer(bg, subj, res.Challenge.ID, answer("alice", pwCanary))
	if err == nil || strings.Contains(err.Error(), pwCanary) || strings.Contains(err.Error(), csCanary) {
		t.Fatalf("a secret leaked in the error: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream said") {
		t.Fatalf("the error lost its useful text: %v", err)
	}
	// A transient provider failure does not burn the request.
	leaky.OnContinue = func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
		return credential.Result{Token: &credential.Token{Value: "t", Expiry: r.clk.Now().Add(time.Hour)}}, nil
	}
	if res, err := svc.Answer(bg, subj, res.Challenge.ID, answer("alice", pwCanary)); err != nil || res.Token == nil {
		t.Fatalf("retry after a provider error: %+v, %v", res, err)
	}
}

func TestInvalidGrantAndExpiredFromProviderEndTheRequest(t *testing.T) {
	for _, perr := range []error{credential.ErrInvalidGrant, credential.ErrChallengeExpired} {
		r := newRig(t)
		bad := &credtest.FakeProvider{KindValue: credential.KindOIDC,
			OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
				return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials}}, nil
			},
			OnContinue: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
				return credential.Result{}, perr
			}}
		svc := r.newService(bad)
		res, _ := svc.Acquire(bg, subj, desc)
		if _, err := svc.Answer(bg, subj, res.Challenge.ID, answer("a", "b")); !errors.Is(err, perr) {
			t.Fatalf("%v: got %v", perr, err)
		}
		if _, err := svc.Answer(bg, subj, res.Challenge.ID, answer("a", "b")); !errors.Is(err, credential.ErrChallengeExpired) {
			t.Fatalf("%v: the request survived: %v", perr, err)
		}
	}
}

func TestNextChallengeGetsANewID(t *testing.T) {
	r := newRig(t)
	two := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials}}, nil
		},
		OnContinue: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeSecretFields, Fields: []credential.Field{{Name: "otp"}}}}, nil
		}}
	svc := r.newService(two)
	first, _ := svc.Acquire(bg, subj, desc)
	next, err := svc.Answer(bg, subj, first.Challenge.ID, answer("alice", pwCanary))
	if err != nil || next.Challenge == nil || next.Challenge.ID == first.Challenge.ID || next.Challenge.Kind != credential.ChallengeSecretFields {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if _, err := svc.Answer(bg, subj, first.Challenge.ID, nil); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("the old id still works: %v", err)
	}
}

func TestConfirmExecChallengeHidesEnvValues(t *testing.T) {
	r := newRig(t)
	prov := &credtest.FakeProvider{KindValue: credential.KindExec,
		OnBegin: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeConfirmExec, Exec: req.Descriptor.Exec}}, nil
		}}
	svc := r.newService(prov)
	d := credential.Descriptor{Kind: credential.KindExec, Exec: &credential.ExecSpec{Command: "tool", Env: map[string]string{"TOKEN": "CANARY-env-value"}}}
	res, err := svc.Acquire(bg, subj, d)
	if err != nil || res.Challenge == nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	if v, ok := res.Challenge.Exec.Env["TOKEN"]; !ok || v != "" {
		t.Fatalf("env = %#v", res.Challenge.Exec.Env)
	}
	if d.Exec.Env["TOKEN"] != "CANARY-env-value" {
		t.Fatal("the caller's descriptor was modified")
	}
}

func TestRegistryRouting(t *testing.T) {
	mk := func(k credential.Kind) *credtest.FakeProvider { return &credtest.FakeProvider{KindValue: k} }
	native, execP := mk(credential.KindEKS), mk(credential.KindExec)
	eks := credential.Descriptor{Kind: credential.KindEKS, Exec: &credential.ExecSpec{Command: "aws"}}
	found := func(string) (string, error) { return "/usr/bin/aws", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }

	build := func(desktop bool, look func(string) (string, error), ps ...credential.Provider) *credential.Registry {
		r := credential.NewRegistry(desktop)
		r.SetLookPath(look)
		for _, p := range ps {
			if err := r.Register(p); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	tests := []struct {
		name string
		reg  *credential.Registry
		d    credential.Descriptor
		want credential.Provider
		err  error
	}{
		{"desktop, binary found: exec plugin wins over native", build(true, found, native, execP), eks, execP, nil},
		{"desktop, binary missing: native", build(true, missing, native, execP), eks, native, nil},
		{"web: never the plugin, native", build(false, found, native, execP), eks, native, nil},
		{"web: no native, exec block: manual (the exec provider)", build(false, found, execP), eks, execP, nil},
		{"desktop: no native, binary missing, exec block: exec provider", build(true, missing, execP), eks, execP, nil},
		{"exec kind goes to the exec provider", build(false, found, execP), credential.Descriptor{Kind: credential.KindExec, Exec: &credential.ExecSpec{Command: "x"}}, execP, nil},
		{"no exec block and no native: unsupported", build(false, found, execP), credential.Descriptor{Kind: credential.KindEKS}, nil, credential.ErrUnsupported},
		{"static has no provider", build(true, found, native, execP), credential.Descriptor{Kind: credential.KindStatic}, nil, credential.ErrUnsupported},
		{"descriptor without exec uses native on the desktop", build(true, found, native), credential.Descriptor{Kind: credential.KindEKS}, native, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.reg.Resolve(tc.d)
			if !errors.Is(err, tc.err) || got != tc.want {
				t.Fatalf("Resolve = %v, %v", got, err)
			}
		})
	}
	reg := credential.NewRegistry(false)
	_ = reg.Register(native)
	if err := reg.Register(mk(credential.KindEKS)); !errors.Is(err, credential.ErrProviderRegistered) {
		t.Fatalf("duplicate = %v", err)
	}
}

// The control string: neither the password, nor the client secret, nor a token shows up in logs, errors,
// the pending-request store or the credential store.
func TestSecretsNeverLeak(t *testing.T) {
	r := newRig(t)
	var errs []string
	note := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	ch := r.login()
	_, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", "wrong-"+pwCanary))
	note(err)
	// While the request is pending, the state at rest must not show the secrets.
	if d := r.chal.Dump(); bytes.Contains(d, []byte(csCanary)) || bytes.Contains(d, []byte("grant")) {
		t.Fatal("the pending request is stored in the clear")
	}
	res, err := r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary))
	note(err)
	if err != nil || res.Token == nil {
		t.Fatal(err)
	}
	_, err = r.svc.Answer(bg, subj, ch.ID, answer("alice", pwCanary))
	note(err)

	raw := func() []byte {
		b := new(bytes.Buffer)
		for _, f := range []string{r.storeP} {
			data, _ := os.ReadFile(f)
			b.Write(data)
		}
		b.Write(r.chal.Dump())
		return b.Bytes()
	}()
	if len(raw) == 0 {
		t.Fatal("nothing at rest: the check proves nothing")
	}
	for _, secret := range []string{pwCanary, csCanary, res.Token.Value} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("%q found at rest", secret)
		}
		if strings.Contains(r.logs.String(), secret) {
			t.Errorf("%q found in the logs", secret)
		}
		for _, e := range errs {
			if strings.Contains(e, secret) {
				t.Errorf("%q found in an error: %s", secret, e)
			}
		}
	}
	if r.logs.Len() == 0 {
		t.Fatal("no logs were produced: the check proves nothing")
	}
}

// A value that cannot be used at all (ErrInvalidAnswer) is not a wrong guess: it uses up no attempt, the request
// stays open, and the caller gets the same sentinel (not a reworded text, not ErrAnswerRejected).
func TestInvalidAnswerKeepsTheRequestOpenAndUsesNoAttempt(t *testing.T) {
	r := newRig(t)
	bad := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeManualToken, Fields: []credential.Field{{Name: "token", Secret: true}}}}, nil
		},
		OnContinue: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			if req.Answer["token"] == "" {
				return credential.Result{}, credential.ErrInvalidAnswer
			}
			return credential.Result{Token: &credential.Token{Value: "t", Expiry: r.clk.Now().Add(time.Hour)}}, nil
		}}
	svc := r.newService(bad)
	res, err := svc.Acquire(bg, subj, desc)
	if err != nil || res.Challenge == nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	// Far more empty answers than MaxAnswerAttempts: the request must survive all of them.
	for i := 0; i < credential.MaxAnswerAttempts*3; i++ {
		_, err := svc.Answer(bg, subj, res.Challenge.ID, answer("", ""))
		if !errors.Is(err, credential.ErrInvalidAnswer) || errors.Is(err, credential.ErrAnswerRejected) {
			t.Fatalf("answer %d: err = %v, want ErrInvalidAnswer", i, err)
		}
		if err.Error() != "the value could not be used" {
			t.Fatalf("the text of the sentinel was changed: %q", err.Error())
		}
	}
	done, err := svc.Answer(bg, subj, res.Challenge.ID, credential.Answer{"token": "abc"})
	if err != nil || done.Token == nil {
		t.Fatalf("the request was destroyed by invalid answers: %+v, %v", done, err)
	}
}

// Wrong credentials still use up attempts: the new sentinel does not weaken the limit.
func TestRejectedAnswersStillUseUpAttempts(t *testing.T) {
	r := newRig(t)
	svc := r.newService(credtest.NewPasswordProvider(credential.KindOIDC, r.idp))
	res, _ := svc.Acquire(bg, subj, desc)
	for i := 0; i < credential.MaxAnswerAttempts; i++ {
		if _, err := svc.Answer(bg, subj, res.Challenge.ID, answer("alice", "wrong-password")); !errors.Is(err, credential.ErrAnswerRejected) {
			t.Fatalf("answer %d: %v", i, err)
		}
	}
	if _, err := svc.Answer(bg, subj, res.Challenge.ID, answer("alice", pwCanary)); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("the request must be destroyed after %d wrong answers: %v", credential.MaxAnswerAttempts, err)
	}
}

// An unreachable identity provider is "the service is unavailable", never "wrong credentials": the request stays open
// and no attempt is used up.
func TestServiceUnavailableIsNotARejection(t *testing.T) {
	r := newRig(t)
	down := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials}}, nil
		},
		OnContinue: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			// the provider wraps the sentinel into its own error with a detail that must not survive
			return credential.Result{}, fmt.Errorf("dial tcp 10.1.2.3:443 (%s): %w", req.Answer["password"], credential.ErrServiceUnavailable)
		}}
	svc := r.newService(down)
	res, _ := svc.Acquire(bg, subj, desc)
	for i := 0; i < credential.MaxAnswerAttempts*2; i++ {
		_, err := svc.Answer(bg, subj, res.Challenge.ID, answer("alice", pwCanary))
		if !errors.Is(err, credential.ErrServiceUnavailable) || errors.Is(err, credential.ErrAnswerRejected) {
			t.Fatalf("answer %d: err = %v", i, err)
		}
		if strings.Contains(err.Error(), pwCanary) || strings.Contains(err.Error(), "10.1.2.3") {
			t.Fatalf("a detail leaked: %v", err)
		}
	}
	// The identity provider is back: the same request completes.
	down.OnContinue = func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
		return credential.Result{Token: &credential.Token{Value: "t", Expiry: r.clk.Now().Add(time.Hour)}}, nil
	}
	if done, err := svc.Answer(bg, subj, res.Challenge.ID, answer("alice", pwCanary)); err != nil || done.Token == nil {
		t.Fatalf("the request did not survive the outage: %+v, %v", done, err)
	}
}

// During token refresh an outage is not a reason to ask the user to sign in again.
func TestServiceUnavailableDuringRefreshIsNotASignInRequest(t *testing.T) {
	r := newRig(t)
	down := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, fmt.Errorf("lookup idp.example: %w", credential.ErrServiceUnavailable)
		}}
	svc := r.newService(down)
	_, err := svc.Token(bg, subj, desc)
	if !errors.Is(err, credential.ErrServiceUnavailable) || errors.Is(err, credential.ErrChallengeRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestAcquireFallsBackToExecWhenNativeBeginIsUnsupported(t *testing.T) {
	r := newRig(t)
	native := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, credential.ErrUnsupported
		}}
	execProvider := &credtest.FakeProvider{KindValue: credential.KindExec,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeManualToken}}, nil
		}}
	reg := credential.NewRegistry(false)
	if err := reg.Register(native); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(execProvider); err != nil {
		t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env, Now: r.clk.Now})
	if err != nil {
		t.Fatal(err)
	}

	d := desc
	d.Exec = &credential.ExecSpec{Command: "kubectl-something"}
	res, err := svc.Acquire(bg, subj, d)
	if err != nil || res.Challenge == nil || res.Challenge.Kind != credential.ChallengeManualToken {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	if begins, _ := native.Calls(); begins != 1 {
		t.Fatalf("native Begin calls = %d", begins)
	}
	if begins, _ := execProvider.Calls(); begins != 1 {
		t.Fatalf("exec Begin calls = %d", begins)
	}
	if got := execProvider.LastRequest().Descriptor; got.Kind != d.Kind || got.Exec == nil || got.Exec.Command != d.Exec.Command {
		t.Fatalf("fallback request descriptor = %+v", got)
	}
}

func TestAcquireDoesNotFallbackForOtherNativeErrors(t *testing.T) {
	r := newRig(t)
	native := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, context.Canceled
		}}
	execProvider := &credtest.FakeProvider{KindValue: credential.KindExec}
	reg := credential.NewRegistry(false)
	if err := reg.Register(native); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(execProvider); err != nil {
		t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env})
	if err != nil {
		t.Fatal(err)
	}
	d := desc
	d.Exec = &credential.ExecSpec{Command: "kubectl-something"}
	if _, err := svc.Acquire(bg, subj, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v", err)
	}
	if begins, _ := execProvider.Calls(); begins != 0 {
		t.Fatalf("exec Begin calls = %d", begins)
	}
}

func TestExecProviderUnsupportedDoesNotRecurse(t *testing.T) {
	r := newRig(t)
	execProvider := &credtest.FakeProvider{KindValue: credential.KindExec,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, credential.ErrUnsupported
		}}
	reg := credential.NewRegistry(false)
	if err := reg.Register(execProvider); err != nil {
		t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env})
	if err != nil {
		t.Fatal(err)
	}
	d := credential.Descriptor{Kind: credential.KindExec, Exec: &credential.ExecSpec{Command: "kubectl-something"}}
	if _, err := svc.Acquire(bg, subj, d); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("Acquire error = %v", err)
	}
	if begins, _ := execProvider.Calls(); begins != 1 {
		t.Fatalf("exec Begin calls = %d", begins)
	}
}

func TestContinueUnsupportedDoesNotFallback(t *testing.T) {
	r := newRig(t)
	native := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials}}, nil
		},
		OnContinue: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, credential.ErrUnsupported
		}}
	execProvider := &credtest.FakeProvider{KindValue: credential.KindExec}
	reg := credential.NewRegistry(false)
	if err := reg.Register(native); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(execProvider); err != nil {
		t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env})
	if err != nil {
		t.Fatal(err)
	}
	d := desc
	d.Exec = &credential.ExecSpec{Command: "kubectl-something"}
	res, err := svc.Acquire(bg, subj, d)
	if err != nil || res.Challenge == nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	if _, err := svc.Answer(bg, subj, res.Challenge.ID, credential.Answer{"username": "alice"}); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("Answer error = %v", err)
	}
	if _, calls := execProvider.Calls(); calls != 0 {
		t.Fatalf("exec Continue calls = %d", calls)
	}
}

func TestAcquireKeepsUnsupportedWhenExecFallbackIsUnavailable(t *testing.T) {
	r := newRig(t)
	native := &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			return credential.Result{}, credential.ErrUnsupported
		}}
	reg := credential.NewRegistry(false)
	if err := reg.Register(native); err != nil {
		t.Fatal(err)
	}
	svc, err := credential.NewService(credential.Config{Registry: reg, Store: r.store, Challenges: r.chal, Envelope: r.env})
	if err != nil {
		t.Fatal(err)
	}
	d := desc
	d.Exec = &credential.ExecSpec{Command: "kubectl-something"}
	if _, err := svc.Acquire(bg, subj, d); !errors.Is(err, credential.ErrUnsupported) {
		t.Fatalf("Acquire error = %v", err)
	}
	if begins, _ := native.Calls(); begins != 1 {
		t.Fatalf("native Begin calls = %d", begins)
	}
}
