// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/credtest"
	"github.com/24DSO-labs/kubron/internal/secrets"
)

func TestChallengeStoreContract(t *testing.T) {
	credtest.ChallengeStoreContract(t, func(t *testing.T) credtest.ChallengeHarness {
		s := newTestStore(t, keyString("k1"))
		return credtest.ChallengeHarness{
			Store:   s.Challenges(),
			Prepare: func(t *testing.T, id string) { ensureCluster(t, s, id) },
			Second:  func(t *testing.T) credential.ChallengeStore { return openSecond(t, s.keys).Challenges() },
		}
	})
}

func TestCredentialAttemptLimitsAreSharedAcrossPostgresStores(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	a := s.Challenges()
	b := openSecond(t, s.keys).Challenges()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if _, err := a.Fail(ctx, "opaque-user-hash", "opaque-idp-hash", now, 15*time.Minute, 2, 3, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if wait, err := b.Check(ctx, "opaque-user-hash", "opaque-idp-hash", now); err != nil || wait != 5*time.Minute {
		t.Fatalf("shared user lockout = %s, %v", wait, err)
	}

	if err := a.Reset(ctx, "opaque-user-hash", "opaque-idp-hash"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		userKey := fmt.Sprintf("opaque-user-%d", i)
		if _, err := a.Fail(ctx, userKey, "opaque-idp-hash", now, 15*time.Minute, 10, 3, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if wait, err := b.Check(ctx, "another-user", "opaque-idp-hash", now); err != nil || wait != 5*time.Minute {
		t.Fatalf("shared IdP lockout = %s, %v", wait, err)
	}

	var stored string
	if err := s.pool.QueryRow(ctx, `SELECT string_agg(dimension || ':' || key_hash, ' ') FROM credential_attempt_limits`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"alice", "login.example.test", "another-user"} {
		if strings.Contains(stored, raw) {
			t.Errorf("limiter stored raw identity %q: %s", raw, stored)
		}
	}
}

func TestChallengesDieWithTheCluster(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")
	rec := credential.ChallengeRecord{ID: "a", ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", Status: credential.ChallengeActive,
		ExpiresAt: time.Now().Add(time.Minute), CreatedAt: time.Now(), KeyID: "k", State: []byte("x")}
	if err := s.Challenges().Create(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := s.Clusters().Delete("c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Challenges().Get(ctx, "a"); !errors.Is(err, credential.ErrChallengeNotFound) {
		t.Fatalf("after cluster delete: %v", err)
	}
	rec.ClusterID = "nocluster"
	if err := s.Challenges().Create(ctx, rec); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("unknown cluster: %v", err)
	}
}

// Two replicas, one database: the completion of a pending request happens exactly once, the credential
// is saved, and neither the password nor the client secret is readable in either table.
func TestServicesOverOnePostgresCompleteOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")

	now := func() time.Time { return time.Now() }
	idp := credtest.NewFakeIdP(now)
	idp.AddUser("alice", "CANARY-pg-password")
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
		svc, err := credential.NewService(credential.Config{Registry: reg, Store: st.Credentials(), Challenges: st.Challenges(), Envelope: env})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	a, b := newSvc(s), newSvc(openSecond(t, s.keys))

	sub := credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1", SessionID: "s1"}
	d := credential.Descriptor{Kind: credential.KindOIDC, Params: map[string]string{"client-secret": "CANARY-pg-client-secret"}}
	res, err := a.Acquire(ctx, sub, d) // the request starts on replica A
	if err != nil || res.Challenge == nil {
		t.Fatalf("Acquire = %+v, %v", res, err)
	}
	id := res.Challenge.ID

	var ok, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := a
			if i%2 == 1 {
				svc = b // ... and is completed on either replica
			}
			r, err := svc.Answer(ctx, sub, id, credential.Answer{"username": "alice", "password": "CANARY-pg-password"})
			switch {
			case err == nil && r.Token != nil:
				ok.Add(1)
			case errors.Is(err, credential.ErrChallengeExpired):
				rejected.Add(1)
			default:
				t.Errorf("unexpected: %+v, %v", r, err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || rejected.Load() != 11 {
		t.Fatalf("completions = %d, rejected = %d", ok.Load(), rejected.Load())
	}
	if _, c := prov.Calls(); c != 1 {
		t.Fatalf("Continue ran %d times", c)
	}

	// A wrong session on the other replica is rejected as well.
	res2, _ := a.Acquire(ctx, credential.Subject{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u2", SessionID: "s2"}, d)
	if res2.Challenge == nil {
		t.Fatal("no second challenge")
	}
	if _, err := b.Answer(ctx, credential.Subject{OwnerID: "u2", SessionID: "other"}, res2.Challenge.ID, credential.Answer{"username": "alice", "password": "CANARY-pg-password"}); !errors.Is(err, credential.ErrChallengeExpired) {
		t.Fatalf("foreign session on replica B: %v", err)
	}

	// Nothing secret at rest in the database (both tables, every column).
	var dump bytes.Buffer
	for _, q := range []string{
		`SELECT coalesce(string_agg(t::text, ' '), '') FROM credential_challenges t`,
		`SELECT coalesce(string_agg(t::text, ' '), '') FROM cluster_credentials t`,
	} {
		var part string
		if err := s.pool.QueryRow(ctx, q).Scan(&part); err != nil {
			t.Fatal(err)
		}
		dump.WriteString(part)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM credential_challenges) + (SELECT count(*) FROM cluster_credentials)`).Scan(&n)
	if n == 0 || dump.Len() == 0 {
		t.Fatal("nothing stored: the check proves nothing")
	}
	for _, secret := range []string{"CANARY-pg-password", "CANARY-pg-client-secret"} {
		if bytes.Contains(dump.Bytes(), []byte(secret)) {
			t.Errorf("%q is readable in the database", secret)
		}
	}
}
