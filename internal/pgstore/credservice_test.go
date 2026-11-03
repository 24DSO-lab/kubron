// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/credtest"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

// Two credential.Service instances (replicas) over ONE PostgreSQL refresh the same subject over and over while
// the identity provider accepts every refresh token only once. No sign-in request may appear and the saved
// refresh token must always be the newest one: no rotation is lost.
func TestTwoServicesOverOnePostgresDoNotLoseRefreshRotation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")

	var mu sync.Mutex
	clock := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }

	idp := credtest.NewFakeIdP(now)
	idp.AccessTTL = time.Minute
	idp.AddUser("alice", "pw")
	prov := credtest.NewPasswordProvider(credential.KindOIDC, idp)

	newSvc := func(st *Store) *credential.Service {
		env, err := secrets.NewEnvelope(st.keys)
		if err != nil {
			t.Fatal(err)
		}
		reg := credential.NewRegistry(false)
		if err := reg.Register(prov); err != nil {
			t.Fatal(err)
		}
		svc, err := credential.NewService(credential.Config{Registry: reg, Store: st.Credentials(), Challenges: st.Challenges(),
			Envelope: env, Now: now, RefreshRecheck: []time.Duration{5 * time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	replicas := []*credential.Service{newSvc(s), newSvc(openSecond(t, s.keys))}

	sub := credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "s1"}
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"grant": "password"}}
	res, err := replicas[0].Acquire(ctx, sub, d)
	if err != nil || res.Challenge == nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	if r, err := replicas[1].Answer(ctx, sub, res.Challenge.ID, credential.Answer{"username": "alice", "password": "pw"}); err != nil || r.Token == nil {
		t.Fatalf("Answer = %+v, %v", r, err)
	}

	var failures atomic.Int32
	for round := 0; round < 20; round++ {
		advance(2 * time.Minute) // the cached token of both replicas is stale
		var wg sync.WaitGroup
		for i := range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := replicas[i%2].Token(ctx, sub, d); err != nil {
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
	rec, err := s.Credentials().Get(ctx, credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(rec.Secret) != idp.LatestRefresh("alice") {
		t.Fatalf("stored %q, the IdP's newest is %q: a rotation was lost", rec.Secret, idp.LatestRefresh("alice"))
	}
	if rec.Version < 21 {
		t.Fatalf("version = %d: rotations were not all written", rec.Version)
	}
	if _, c := prov.Calls(); c != 1 {
		t.Fatalf("unexpected number of interactive continues: %d", c)
	}
}
