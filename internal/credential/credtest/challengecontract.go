// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credtest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// ChallengeHarness gives the contract suite what it needs from one ChallengeStore implementation.
type ChallengeHarness struct {
	Store credential.ChallengeStore
	// Prepare is called before a cluster id is used (the PostgreSQL schema may reference clusters).
	Prepare func(t *testing.T, clusterID string)
	// Second returns another instance over the same data (another replica); may be nil.
	Second func(t *testing.T) credential.ChallengeStore
}

func chRec(id, cluster, owner, session string, exp time.Time) credential.ChallengeRecord {
	return credential.ChallengeRecord{ID: id, ClusterID: cluster, Scope: credential.ScopeUser, OwnerID: owner, SessionID: session,
		Status: credential.ChallengeActive, ExpiresAt: exp, CreatedAt: exp.Add(-10 * time.Minute), KeyID: "k1", State: []byte("state-" + id)}
}

// ChallengeStoreContract runs the contract tests of credential.ChallengeStore.
func ChallengeStoreContract(t *testing.T, newHarness func(t *testing.T) ChallengeHarness) {
	ctx := context.Background()
	exp := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	run := func(name string, fn func(t *testing.T, h ChallengeHarness)) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if h.Prepare != nil {
				for _, c := range []string{"c1", "c2"} {
					h.Prepare(t, c)
				}
			}
			fn(t, h)
		})
	}

	run("CreateGet", func(t *testing.T, h ChallengeHarness) {
		if err := h.Store.Create(ctx, chRec("a", "c1", "u1", "s1", exp)); err != nil {
			t.Fatal(err)
		}
		got, err := h.Store.Get(ctx, "a")
		if err != nil || got.Version != 1 || got.ClusterID != "c1" || got.OwnerID != "u1" || got.SessionID != "s1" ||
			got.Scope != credential.ScopeUser || got.Status != credential.ChallengeActive || !bytes.Equal(got.State, []byte("state-a")) ||
			!got.ExpiresAt.Equal(exp) || got.KeyID != "k1" || got.Attempts != 0 {
			t.Fatalf("got = %+v, %v", got, err)
		}
		if err := h.Store.Create(ctx, chRec("a", "c1", "u1", "s1", exp)); err == nil {
			t.Fatal("a duplicate id was accepted")
		}
		if _, err := h.Store.Get(ctx, "missing"); !errors.Is(err, credential.ErrChallengeNotFound) {
			t.Fatalf("Get(missing) = %v", err)
		}
	})

	run("EmptySessionIsStored", func(t *testing.T, h ChallengeHarness) { // desktop: no session
		if err := h.Store.Create(ctx, chRec("d", "c1", "u1", "", exp)); err != nil {
			t.Fatal(err)
		}
		if got, err := h.Store.Get(ctx, "d"); err != nil || got.SessionID != "" {
			t.Fatalf("got = %+v, %v", got, err)
		}
	})

	run("SwapIsCompareAndSwap", func(t *testing.T, h ChallengeHarness) {
		_ = h.Store.Create(ctx, chRec("a", "c1", "u1", "s1", exp))
		next := chRec("a", "c1", "u1", "s1", exp)
		next.Status, next.Attempts, next.State = credential.ChallengeBusy, 2, []byte("new")
		next.LockedUntil = exp
		got, err := h.Store.Swap(ctx, next, 1)
		if err != nil || got.Version != 2 || got.Status != credential.ChallengeBusy || got.Attempts != 2 || string(got.State) != "new" {
			t.Fatalf("Swap = %+v, %v", got, err)
		}
		if _, err := h.Store.Swap(ctx, next, 1); !errors.Is(err, credential.ErrVersionConflict) {
			t.Fatalf("stale Swap = %v", err)
		}
		if _, err := h.Store.Swap(ctx, chRec("zz", "c1", "u1", "s1", exp), 1); !errors.Is(err, credential.ErrChallengeNotFound) {
			t.Fatalf("Swap of a missing record = %v", err)
		}
		stored, _ := h.Store.Get(ctx, "a")
		if stored.Version != 2 || string(stored.State) != "new" {
			t.Fatalf("stored = %+v", stored)
		}
	})

	run("SwapNeverChangesTheSubject", func(t *testing.T, h ChallengeHarness) {
		_ = h.Store.Create(ctx, chRec("a", "c1", "u1", "s1", exp))
		hijack := chRec("a", "c2", "attacker", "s9", exp)
		hijack.Scope = credential.ScopeTeam
		if _, err := h.Store.Swap(ctx, hijack, 1); err != nil {
			t.Fatal(err)
		}
		got, _ := h.Store.Get(ctx, "a")
		if got.ClusterID != "c1" || got.OwnerID != "u1" || got.SessionID != "s1" || got.Scope != credential.ScopeUser {
			t.Fatalf("the subject was rewritten: %+v", got)
		}
	})

	run("ExactlyOneWinnerPerVersion", func(t *testing.T, h ChallengeHarness) {
		_ = h.Store.Create(ctx, chRec("a", "c1", "u1", "s1", exp))
		stores := []credential.ChallengeStore{h.Store}
		if h.Second != nil {
			stores = append(stores, h.Second(t))
		}
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				claim := chRec("a", "c1", "u1", "s1", exp)
				claim.Status = credential.ChallengeBusy
				if _, err := stores[i%len(stores)].Swap(ctx, claim, 1); err == nil {
					wins.Add(1)
				} else if !errors.Is(err, credential.ErrVersionConflict) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%d claims succeeded, want exactly 1", wins.Load())
		}
	})

	run("StateIsCopied", func(t *testing.T, h ChallengeHarness) {
		rec := chRec("a", "c1", "u1", "s1", exp)
		_ = h.Store.Create(ctx, rec)
		rec.State[0] = 'X' // the caller's slice must not alias the store
		got, _ := h.Store.Get(ctx, "a")
		if got.State[0] == 'X' {
			t.Fatal("the store aliases the caller's slice")
		}
		got.State[0] = 'Y'
		again, _ := h.Store.Get(ctx, "a")
		if again.State[0] == 'Y' {
			t.Fatal("Get returns the store's own slice")
		}
	})

	run("Delete", func(t *testing.T, h ChallengeHarness) {
		_ = h.Store.Create(ctx, chRec("a", "c1", "u1", "s1", exp))
		if err := h.Store.Delete(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if err := h.Store.Delete(ctx, "a"); err != nil {
			t.Fatalf("second Delete: %v", err)
		}
		if _, err := h.Store.Get(ctx, "a"); !errors.Is(err, credential.ErrChallengeNotFound) {
			t.Fatalf("after Delete: %v", err)
		}
	})

	run("DeleteBy", func(t *testing.T, h ChallengeHarness) {
		_ = h.Store.Create(ctx, chRec("a", "c1", "u1", "s", exp))
		_ = h.Store.Create(ctx, chRec("b", "c1", "u2", "s", exp))
		_ = h.Store.Create(ctx, chRec("c", "c2", "u1", "s", exp))
		team := chRec("d", "c1", "u1", "s", exp)
		team.Scope = credential.ScopeTeam
		_ = h.Store.Create(ctx, team)
		exists := func(id string) bool { _, err := h.Store.Get(ctx, id); return err == nil }
		if n, err := h.Store.DeleteBy(ctx, credential.RevokeFilter{ClusterID: "c1", OwnerID: "u1", Scope: credential.ScopeUser}); err != nil || n != 1 || exists("a") || !exists("d") {
			t.Fatalf("by cluster+owner+scope: %d, %v", n, err)
		}
		if n, err := h.Store.DeleteBy(ctx, credential.RevokeFilter{OwnerID: "u1"}); err != nil || n != 2 || exists("c") || exists("d") || !exists("b") {
			t.Fatalf("by owner: %d, %v", n, err)
		}
		if n, err := h.Store.DeleteBy(ctx, credential.RevokeFilter{ClusterID: "c1"}); err != nil || n != 1 || exists("b") {
			t.Fatalf("by cluster: %d, %v", n, err)
		}
	})

	run("DeleteExpired", func(t *testing.T, h ChallengeHarness) {
		now := time.Now().UTC()
		_ = h.Store.Create(ctx, chRec("old", "c1", "u1", "s", now.Add(-time.Minute)))
		_ = h.Store.Create(ctx, chRec("new", "c1", "u1", "s", now.Add(time.Minute)))
		if n, err := h.Store.DeleteExpired(ctx, now); err != nil || n != 1 {
			t.Fatalf("DeleteExpired = %d, %v", n, err)
		}
		if _, err := h.Store.Get(ctx, "old"); !errors.Is(err, credential.ErrChallengeNotFound) {
			t.Fatalf("expired record survived: %v", err)
		}
		if _, err := h.Store.Get(ctx, "new"); err != nil {
			t.Fatalf("live record deleted: %v", err)
		}
	})
}
