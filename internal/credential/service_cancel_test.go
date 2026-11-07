// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/credtest"
)

// listenerProvider is a provider with something outside of Pending to release (the loopback listener of the
// desktop): it implements the optional Cancel and Close.
type listenerProvider struct {
	*credtest.FakeProvider
	mu        sync.Mutex
	open      map[string]bool // "listeners" by the Pending that names them
	cancelled []string
	closed    int
	n         int
	done      bool // the "browser" has come back
}

func newListenerProvider(r *rig) *listenerProvider {
	lp := &listenerProvider{open: map[string]bool{}}
	lp.FakeProvider = &credtest.FakeProvider{KindValue: credential.KindOIDC,
		OnBegin: func(context.Context, credential.Env, credential.Request) (credential.Result, error) {
			lp.mu.Lock()
			defer lp.mu.Unlock()
			lp.n++
			id := "listener-" + string(rune('0'+lp.n))
			lp.open[id] = true
			return credential.Result{
				Challenge: &credential.Challenge{Kind: credential.ChallengeBrowserLoopback, Poll: true, IntervalSec: 1},
				Pending:   []byte(id),
			}, nil
		},
		OnContinue: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			lp.mu.Lock()
			done := lp.done
			lp.mu.Unlock()
			if done {
				return credential.Result{Token: &credential.Token{Value: "t", Expiry: r.clk.Now().Add(time.Hour)}}, nil
			}
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeBrowserLoopback, Poll: true, IntervalSec: 1}, Pending: req.Pending}, nil
		}}
	return lp
}

func (l *listenerProvider) Cancel(pending []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelled = append(l.cancelled, string(pending))
	delete(l.open, string(pending))
}

func (l *listenerProvider) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed++
	clear(l.open)
}

func (l *listenerProvider) openCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.open)
}

func (l *listenerProvider) cancels() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.cancelled...)
}

func startListener(t *testing.T) (*rig, *credential.Service, *listenerProvider, credential.Challenge) {
	t.Helper()
	r := newRig(t)
	lp := newListenerProvider(r)
	svc := r.newService(lp)
	res, err := svc.Acquire(bg, subj, desc)
	if err != nil || res.Challenge == nil || lp.openCount() != 1 {
		t.Fatalf("Acquire = %+v, %v (listeners: %d)", res, err, lp.openCount())
	}
	return r, svc, lp, *res.Challenge
}

func TestCancelChallengeReleasesTheProviderAtOnce(t *testing.T) {
	_, svc, lp, ch := startListener(t)
	if err := svc.Cancel(bg, subj, ch.ID); err != nil {
		t.Fatal(err)
	}
	if got := lp.cancels(); len(got) != 1 || got[0] != "listener-1" || lp.openCount() != 0 {
		t.Fatalf("cancels = %v, open listeners = %d", got, lp.openCount())
	}
	if _, err := svc.Poll(bg, subj, ch.ID); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("poll after cancel: %v", err)
	}
}

func TestCancelOfSomebodyElsesRequestReleasesNothing(t *testing.T) {
	_, svc, lp, ch := startListener(t)
	if err := svc.Cancel(bg, credential.Subject{OwnerID: "u2", SessionID: "s1"}, ch.ID); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("foreign cancel: %v", err)
	}
	if len(lp.cancels()) != 0 || lp.openCount() != 1 {
		t.Fatal("a foreign cancel released the listener")
	}
}

func TestExpiredRequestReleasesTheProvider(t *testing.T) {
	r, svc, lp, ch := startListener(t)
	r.clk.Advance(credential.ChallengeTTL + time.Second)
	if _, err := svc.Poll(bg, subj, ch.ID); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("poll after expiry: %v", err)
	}
	if got := lp.cancels(); len(got) != 1 || got[0] != "listener-1" || lp.openCount() != 0 {
		t.Fatalf("cancels = %v, open listeners = %d", got, lp.openCount())
	}
}

func TestSweepReleasesExpiredRequestsOnly(t *testing.T) {
	r, svc, lp, first := startListener(t)
	// a second request, created later, must survive the sweep
	r.clk.Advance(credential.ChallengeTTL - time.Minute)
	other := subj
	other.OwnerID = "u2"
	other.SessionID = "s2"
	res, err := svc.Acquire(bg, other, desc)
	if err != nil || res.Challenge == nil {
		t.Fatal(err)
	}
	r.clk.Advance(2 * time.Minute) // the first one has expired, the second has not
	if _, err := svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if got := lp.cancels(); len(got) != 1 || got[0] != "listener-1" {
		t.Fatalf("cancels after Sweep = %v", got)
	}
	if lp.openCount() != 1 {
		t.Fatalf("open listeners = %d, want 1 (the live request)", lp.openCount())
	}
	_ = first
}

// The wait for the browser ends well: the request is completed and the provider is told as well (it is idempotent
// for the provider and makes sure that nothing is left behind).
func TestCompletedRequestReleasesTheProvider(t *testing.T) {
	_, svc, lp, ch := startListener(t)
	lp.mu.Lock()
	lp.done = true
	lp.mu.Unlock()
	res, err := svc.Poll(bg, subj, ch.ID)
	if err != nil || res.Token == nil {
		t.Fatalf("Poll = %+v, %v", res, err)
	}
	if lp.openCount() != 0 {
		t.Fatalf("open listeners after completion = %d", lp.openCount())
	}
}

func TestStillWaitingKeepsTheListener(t *testing.T) {
	_, svc, lp, ch := startListener(t)
	for range 3 {
		if res, err := svc.Poll(bg, subj, ch.ID); err != nil || res.Challenge == nil || res.Challenge.ID != ch.ID {
			t.Fatalf("Poll = %+v, %v", res, err)
		}
	}
	if len(lp.cancels()) != 0 || lp.openCount() != 1 {
		t.Fatalf("a request that is still waiting was released: %v", lp.cancels())
	}
}

func TestRevokeReleasesThePendingRequestsOfTheOwner(t *testing.T) {
	_, svc, lp, _ := startListener(t)
	if err := svc.Revoke(bg, credential.RevokeFilter{OwnerID: "u-someone-else"}); err != nil {
		t.Fatal(err)
	}
	if lp.openCount() != 1 {
		t.Fatal("a revoke of another owner released the listener")
	}
	if err := svc.Revoke(bg, credential.RevokeFilter{OwnerID: subj.OwnerID}); err != nil {
		t.Fatal(err)
	}
	if lp.openCount() != 0 {
		t.Fatal("a revoke of the owner left the listener open")
	}
}

func TestCloseReleasesEverything(t *testing.T) {
	_, svc, lp, _ := startListener(t)
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	if lp.openCount() != 0 || lp.closed != 1 {
		t.Fatalf("open listeners = %d, Close calls = %d", lp.openCount(), lp.closed)
	}
	if err := svc.Close(); err != nil { // twice is fine
		t.Fatal(err)
	}
}

// A provider that implements neither interface is not affected.
func TestProvidersWithoutCancelAreUnaffected(t *testing.T) {
	r := newRig(t)
	ch := r.login()
	if err := r.svc.Cancel(bg, subj, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Close(); err != nil {
		t.Fatal(err)
	}
}
