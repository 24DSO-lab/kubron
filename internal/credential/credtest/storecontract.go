// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/secretbox"
)

// Canary is a marker string for "secrets must not leak" tests.
const Canary = "CANARY-7f3a91c2-do-not-leak"

// StoreHarness gives the contract suite what it needs from one implementation.
type StoreHarness struct {
	Store credential.CredentialStore
	// Second returns another store instance over the same data (a second replica or process).
	Second func(t *testing.T) credential.CredentialStore
	// Prepare is called before a cluster id is used (the PostgreSQL schema has a foreign key to clusters).
	Prepare func(t *testing.T, clusterID string)
	// Dump returns everything the implementation keeps at rest, for scanning for plaintext.
	Dump func(t *testing.T) []byte
	// RawBlob and SetRawBlob read and replace the stored ciphertext of a record.
	RawBlob    func(t *testing.T, k credential.Key) (blob []byte, keyID string)
	SetRawBlob func(t *testing.T, k credential.Key, blob []byte, keyID string)
}

func (h StoreHarness) prep(t *testing.T, ids ...string) {
	t.Helper()
	if h.Prepare == nil {
		return
	}
	for _, id := range ids {
		h.Prepare(t, id)
	}
}

func userKey(cluster, owner string) credential.Key {
	return credential.Key{ClusterID: cluster, Scope: credential.ScopeUser, OwnerID: owner}
}
func teamKey(cluster, owner string) credential.Key {
	return credential.Key{ClusterID: cluster, Scope: credential.ScopeTeam, OwnerID: owner}
}

func rec(k credential.Key, secret string) credential.Record {
	return credential.Record{Meta: credential.Meta{Key: k, Kind: credential.KindOIDC, Principal: "sub-" + k.OwnerID}, Secret: []byte(secret)}
}

// StoreContract runs the contract tests. newHarness must return an empty store every time.
func StoreContract(t *testing.T, newHarness func(t *testing.T) StoreHarness) {
	ctx := context.Background()
	run := func(name string, fn func(t *testing.T, h StoreHarness)) {
		t.Run(name, func(t *testing.T) { fn(t, newHarness(t)) })
	}

	run("PutGetRoundTrip", func(t *testing.T, h StoreHarness) {
		k := userKey("c1", "u1")
		h.prep(t, "c1")
		exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
		r := rec(k, Canary+"-refresh")
		r.ExpiresAt = &exp
		m, err := h.Store.Put(ctx, r, 0)
		if err != nil {
			t.Fatal(err)
		}
		if m.Version != 1 || m.Key != k || m.KeyID == "" {
			t.Fatalf("meta = %+v", m)
		}
		got, err := h.Store.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Secret) != Canary+"-refresh" || got.Kind != credential.KindOIDC || got.Principal != "sub-u1" || got.Version != 1 {
			t.Fatalf("got = %+v", got)
		}
		if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
			t.Fatalf("expiresAt = %v, want %v", got.ExpiresAt, exp)
		}
		if got.UpdatedAt.IsZero() {
			t.Fatal("updatedAt is zero")
		}
	})

	run("NotFound", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1")
		if _, err := h.Store.Get(ctx, userKey("c1", "nobody")); !errors.Is(err, credential.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	run("EmptySecret", func(t *testing.T, h StoreHarness) {
		k := teamKey("c1", "t1")
		h.prep(t, "c1")
		if _, err := h.Store.Put(ctx, rec(k, ""), 0); err != nil {
			t.Fatal(err)
		}
		got, err := h.Store.Get(ctx, k)
		if err != nil || len(got.Secret) != 0 {
			t.Fatalf("got = %+v, err = %v", got, err)
		}
	})

	run("OptimisticLocking", func(t *testing.T, h StoreHarness) {
		k := userKey("c1", "u1")
		h.prep(t, "c1")
		if _, err := h.Store.Put(ctx, rec(k, "v1"), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Store.Put(ctx, rec(k, "dup"), 0); !errors.Is(err, credential.ErrVersionConflict) {
			t.Fatalf("second insert: %v, want ErrVersionConflict", err)
		}
		m, err := h.Store.Put(ctx, rec(k, "v2"), 1)
		if err != nil || m.Version != 2 {
			t.Fatalf("update: %+v, %v", m, err)
		}
		if _, err := h.Store.Put(ctx, rec(k, "stale"), 1); !errors.Is(err, credential.ErrVersionConflict) {
			t.Fatalf("stale update: %v, want ErrVersionConflict", err)
		}
		if _, err := h.Store.Put(ctx, rec(userKey("c1", "ghost"), "x"), 5); !errors.Is(err, credential.ErrVersionConflict) {
			t.Fatalf("update of a missing record: %v, want ErrVersionConflict", err)
		}
		got, _ := h.Store.Get(ctx, k)
		if string(got.Secret) != "v2" || got.Version != 2 {
			t.Fatalf("a rejected write changed the record: %+v", got)
		}
		m, err = h.Store.Put(ctx, rec(k, "forced"), credential.AnyVersion)
		if err != nil || m.Version != 3 {
			t.Fatalf("upsert: %+v, %v", m, err)
		}
		m, err = h.Store.Put(ctx, rec(userKey("c1", "u2"), "new"), credential.AnyVersion)
		if err != nil || m.Version != 1 {
			t.Fatalf("upsert of a new record: %+v, %v", m, err)
		}
	})

	run("DeleteIsIdempotent", func(t *testing.T, h StoreHarness) {
		k := userKey("c1", "u1")
		h.prep(t, "c1")
		if err := h.Store.Delete(ctx, k); err != nil {
			t.Fatalf("delete of a missing record: %v", err)
		}
		_, _ = h.Store.Put(ctx, rec(k, "x"), 0)
		if err := h.Store.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Store.Get(ctx, k); !errors.Is(err, credential.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
		// The key can be created again, with version 1.
		if m, err := h.Store.Put(ctx, rec(k, "y"), 0); err != nil || m.Version != 1 {
			t.Fatalf("recreate: %+v, %v", m, err)
		}
	})

	run("ScopesAndOwnersAreSeparate", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1", "c2")
		keys := []credential.Key{userKey("c1", "x"), teamKey("c1", "x"), userKey("c1", "y"), userKey("c2", "x")}
		for i, k := range keys {
			if _, err := h.Store.Put(ctx, rec(k, "secret-"+strconv.Itoa(i)), 0); err != nil {
				t.Fatalf("%v: %v", k, err)
			}
		}
		for i, k := range keys {
			got, err := h.Store.Get(ctx, k)
			if err != nil || string(got.Secret) != "secret-"+strconv.Itoa(i) {
				t.Fatalf("%v: %+v, %v", k, got, err)
			}
		}
	})

	run("List", func(t *testing.T, h StoreHarness) {
		h.prep(t, "ca", "cb", "cc")
		for _, k := range []credential.Key{userKey("cb", "u1"), userKey("ca", "u1"), teamKey("cc", "u1"), userKey("ca", "u2")} {
			if _, err := h.Store.Put(ctx, rec(k, Canary), 0); err != nil {
				t.Fatal(err)
			}
		}
		got, err := h.Store.List(ctx, "u1")
		if err != nil || len(got) != 3 {
			t.Fatalf("List = %+v, %v", got, err)
		}
		if got[0].ClusterID != "ca" || got[1].ClusterID != "cb" || got[2].ClusterID != "cc" {
			t.Fatalf("not ordered by cluster: %+v", got)
		}
		if got[2].Scope != credential.ScopeTeam || got[0].Kind != credential.KindOIDC || got[0].Principal != "sub-u1" {
			t.Fatalf("meta = %+v", got)
		}
		if none, err := h.Store.List(ctx, "nobody"); err != nil || len(none) != 0 {
			t.Fatalf("List(nobody) = %+v, %v", none, err)
		}
	})

	run("Revoke", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1", "c2")
		put := func(k credential.Key) {
			if _, err := h.Store.Put(ctx, rec(k, "s"), 0); err != nil {
				t.Fatal(err)
			}
		}
		exists := func(k credential.Key) bool {
			_, err := h.Store.Get(ctx, k)
			return err == nil
		}
		all := []credential.Key{userKey("c1", "u1"), userKey("c1", "u2"), teamKey("c1", "t1"), userKey("c2", "u1")}
		for _, k := range all {
			put(k)
		}
		if n, err := h.Store.Revoke(ctx, credential.RevokeFilter{ClusterID: "c1", OwnerID: "u1"}); err != nil || n != 1 {
			t.Fatalf("revoke cluster+owner: %d, %v", n, err)
		}
		if exists(all[0]) || !exists(all[1]) || !exists(all[3]) {
			t.Fatal("wrong records removed")
		}
		if n, err := h.Store.Revoke(ctx, credential.RevokeFilter{OwnerID: "u1"}); err != nil || n != 1 || exists(all[3]) {
			t.Fatalf("revoke owner: %d, %v", n, err)
		}
		if n, err := h.Store.Revoke(ctx, credential.RevokeFilter{ClusterID: "c1", Scope: credential.ScopeTeam}); err != nil || n != 1 || exists(all[2]) {
			t.Fatalf("revoke cluster+scope: %d, %v", n, err)
		}
		if n, err := h.Store.Revoke(ctx, credential.RevokeFilter{ClusterID: "c1"}); err != nil || n != 1 || exists(all[1]) {
			t.Fatalf("revoke cluster: %d, %v", n, err)
		}
		if n, err := h.Store.Revoke(ctx, credential.RevokeFilter{ClusterID: "c1"}); err != nil || n != 0 {
			t.Fatalf("revoke of nothing: %d, %v", n, err)
		}
	})

	run("InvalidKeysAndSizes", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1")
		bad := []credential.Key{
			{ClusterID: "", Scope: credential.ScopeUser, OwnerID: "u"},
			{ClusterID: "a/b", Scope: credential.ScopeUser, OwnerID: "u"},
			{ClusterID: "c1", Scope: "", OwnerID: "u"},
			{ClusterID: "c1", Scope: "other", OwnerID: "u"},
			{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: ""},
			{ClusterID: strings.Repeat("a", 81), Scope: credential.ScopeUser, OwnerID: "u"},
		}
		for _, k := range bad {
			if _, err := h.Store.Put(ctx, rec(k, "x"), 0); !errors.Is(err, credential.ErrInvalidKey) {
				t.Errorf("Put(%+v) = %v, want ErrInvalidKey", k, err)
			}
			if _, err := h.Store.Get(ctx, k); !errors.Is(err, credential.ErrInvalidKey) {
				t.Errorf("Get(%+v) = %v, want ErrInvalidKey", k, err)
			}
		}
		big := rec(userKey("c1", "u"), strings.Repeat("x", credential.MaxSecretSize+1))
		if _, err := h.Store.Put(ctx, big, 0); !errors.Is(err, credential.ErrSecretTooLarge) {
			t.Errorf("oversized Put = %v", err)
		}
	})

	run("SecretIsEncryptedAtRest", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1")
		k := userKey("c1", "u1")
		if _, err := h.Store.Put(ctx, rec(k, Canary+"-password-and-token"), 0); err != nil {
			t.Fatal(err)
		}
		dump := h.Dump(t)
		if len(dump) == 0 {
			t.Fatal("empty dump: the check proves nothing")
		}
		if bytes.Contains(dump, []byte(Canary)) {
			t.Fatal("plaintext secret found at rest")
		}
	})

	run("CorruptedCiphertextIsAnError", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1")
		k := userKey("c1", "u1")
		if _, err := h.Store.Put(ctx, rec(k, Canary), 0); err != nil {
			t.Fatal(err)
		}
		blob, keyID := h.RawBlob(t, k)
		bad := bytes.Clone(blob)
		bad[len(bad)-1] ^= 0xff
		h.SetRawBlob(t, k, bad, keyID)
		got, err := h.Store.Get(ctx, k)
		if err == nil {
			t.Fatalf("a corrupted record was read as %+v", got)
		}
		if !errors.Is(err, secretbox.ErrDecrypt) || errors.Is(err, credential.ErrNotFound) {
			t.Fatalf("err = %v, want ErrDecrypt", err)
		}
		if got.Secret != nil || strings.Contains(err.Error(), Canary) {
			t.Fatal("secret returned or leaked together with an error")
		}
		h.SetRawBlob(t, k, nil, keyID)
		if _, err := h.Store.Get(ctx, k); err == nil || errors.Is(err, credential.ErrNotFound) {
			t.Fatalf("an empty ciphertext must be an error, got %v", err)
		}
	})

	run("CiphertextIsBoundToItsRecord", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1", "c2")
		a, b := userKey("c1", "u1"), userKey("c2", "u1")
		c, d := userKey("c1", "u2"), teamKey("c1", "u1")
		for _, k := range []credential.Key{a, b, c, d} {
			if _, err := h.Store.Put(ctx, rec(k, "secret-of-"+k.ClusterID+k.OwnerID), 0); err != nil {
				t.Fatal(err)
			}
		}
		blob, keyID := h.RawBlob(t, a)
		for _, other := range []credential.Key{b, c, d} {
			h.SetRawBlob(t, other, blob, keyID)
			if got, err := h.Store.Get(ctx, other); err == nil {
				t.Fatalf("ciphertext of %v was accepted for %v: %q", a, other, got.Secret)
			}
		}
	})

	run("ConcurrentWritersLoseNothing", func(t *testing.T, h StoreHarness) {
		h.prep(t, "c1")
		k := userKey("c1", "u1")
		if _, err := h.Store.Put(ctx, rec(k, "0"), 0); err != nil {
			t.Fatal(err)
		}
		increment(t, ctx, []credential.CredentialStore{h.Store}, k, 8, 10)
		got, err := h.Store.Get(ctx, k)
		if err != nil || string(got.Secret) != "80" || got.Version != 81 {
			t.Fatalf("got = %q v%d, %v; want 80 v81", got.Secret, got.Version, err)
		}
	})

	run("TwoInstancesLoseNothing", func(t *testing.T, h StoreHarness) {
		if h.Second == nil {
			t.Skip("no second instance for this implementation")
		}
		h.prep(t, "c1")
		k := userKey("c1", "u1")
		if _, err := h.Store.Put(ctx, rec(k, "0"), 0); err != nil {
			t.Fatal(err)
		}
		increment(t, ctx, []credential.CredentialStore{h.Store, h.Second(t)}, k, 8, 10)
		got, err := h.Store.Get(ctx, k)
		if err != nil || string(got.Secret) != "80" {
			t.Fatalf("got = %q, %v; want 80", got.Secret, err)
		}
	})
}

// increment runs workers goroutines, spread over the stores, each doing n read-modify-write cycles on a
// numeric secret with optimistic retries. A lost update would make the final number smaller.
func increment(t *testing.T, ctx context.Context, stores []credential.CredentialStore, k credential.Key, workers, n int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		st := stores[w%len(stores)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range n {
				for attempt := 0; ; attempt++ {
					cur, err := st.Get(ctx, k)
					if err != nil {
						errs <- err
						return
					}
					v, _ := strconv.Atoi(string(cur.Secret))
					next := cur
					next.Secret = []byte(strconv.Itoa(v + 1))
					_, err = st.Put(ctx, next, cur.Version)
					if err == nil {
						break
					}
					if !errors.Is(err, credential.ErrVersionConflict) {
						errs <- err
						return
					}
					if attempt > 10000 {
						errs <- fmt.Errorf("starved after %d attempts", attempt)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
