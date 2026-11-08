// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/credential/credtest"
	"github.com/24DSO-lab/kubron/internal/secretbox"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

func newSealer(t *testing.T, cur secretbox.Key, prev ...secretbox.Key) *credential.Sealer {
	t.Helper()
	kr, err := secretbox.NewKeyring(cur, prev...)
	if err != nil {
		t.Fatal(err)
	}
	env, err := secrets.NewEnvelope(kr)
	if err != nil {
		t.Fatal(err)
	}
	return credential.NewSealer(env)
}

func newKey(t *testing.T, id string) secretbox.Key {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k.ID = id
	return k
}

func TestStoreContract(t *testing.T) {
	credtest.StoreContract(t, func(t *testing.T) credtest.StoreHarness {
		path := filepath.Join(t.TempDir(), "credentials.json")
		sealer := newSealer(t, newKey(t, "k1"))
		st, err := New(path, sealer)
		if err != nil {
			t.Fatal(err)
		}
		readDoc := func(t *testing.T) doc {
			t.Helper()
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var d doc
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			return d
		}
		return credtest.StoreHarness{
			Store: st,
			Second: func(t *testing.T) credential.CredentialStore {
				s2, err := New(path, sealer)
				if err != nil {
					t.Fatal(err)
				}
				return s2
			},
			Dump: func(t *testing.T) []byte {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			},
			RawBlob: func(t *testing.T, k credential.Key) ([]byte, string) {
				d := readDoc(t)
				if i := st.find(d, k); i >= 0 {
					return d.Records[i].SecretEnc, d.Records[i].KeyID
				}
				t.Fatalf("record %v not found", k)
				return nil, ""
			},
			SetRawBlob: func(t *testing.T, k credential.Key, blob []byte, keyID string) {
				d := readDoc(t)
				i := st.find(d, k)
				if i < 0 {
					t.Fatalf("record %v not found", k)
				}
				d.Records[i].SecretEnc, d.Records[i].KeyID = blob, keyID
				if err := st.save(d); err != nil {
					t.Fatal(err)
				}
			},
		}
	})
}

func TestFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "credentials.json")
	st, err := New(path, newSealer(t, newKey(t, "k1")))
	if err != nil {
		t.Fatal(err)
	}
	k := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	if _, err := st.Put(context.Background(), credential.Record{Meta: credential.Meta{Key: k, Kind: credential.KindOIDC}, Secret: []byte("x")}, 0); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v", fi.Mode().Perm(), err)
	}
	if _, err := os.Stat(path + ".lock"); err == nil {
		t.Fatal("lock file left behind")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, ".credentials-*.tmp")); len(m) != 0 {
		t.Fatalf("temp files left behind: %v", m)
	}
}

func TestDamagedFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := New(path, newSealer(t, newKey(t, "k1")))
	k := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	if _, err := st.Get(context.Background(), k); err == nil || errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("a damaged file must not look like an empty store: %v", err)
	}
	// And a write must not overwrite (and thereby destroy) the damaged file.
	if _, err := st.Put(context.Background(), credential.Record{Meta: credential.Meta{Key: k}, Secret: []byte("x")}, 0); err == nil {
		t.Fatal("Put replaced a damaged file")
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, []byte("{not json")) {
		t.Fatal("the damaged file was modified")
	}
}

func TestStaleLockIsRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	st, _ := New(path, newSealer(t, newKey(t, "k1")))
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := st.now().Add(-time.Hour)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	k := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	if _, err := st.Put(context.Background(), credential.Record{Meta: credential.Meta{Key: k}, Secret: []byte("x")}, 0); err != nil {
		t.Fatalf("a stale lock blocks writers: %v", err)
	}
}

func TestRekey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credentials.json")
	oldKey, newK := newKey(t, "old"), newKey(t, "new")
	oldStore, _ := New(path, newSealer(t, oldKey))
	keys := []credential.Key{
		{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"},
		{ClusterID: "c2", Scope: credential.ScopeTeam, OwnerID: "t1"},
	}
	for _, k := range keys {
		if _, err := oldStore.Put(ctx, credential.Record{Meta: credential.Meta{Key: k, Kind: credential.KindOIDC}, Secret: []byte("s-" + k.ClusterID)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	rotated, _ := New(path, newSealer(t, newK, oldKey))
	rep, err := rotated.Rekey(ctx)
	if err != nil || rep.Total != 2 || rep.Changed != 2 || len(rep.Failed) != 0 {
		t.Fatalf("Rekey = %+v, %v", rep, err)
	}
	// After the rekey the previous key is no longer needed.
	onlyNew, _ := New(path, newSealer(t, newK))
	for _, k := range keys {
		got, err := onlyNew.Get(ctx, k)
		if err != nil || string(got.Secret) != "s-"+k.ClusterID || got.KeyID != "new" {
			t.Fatalf("%v: %+v, %v", k, got, err)
		}
		if got.Version != 1 {
			t.Fatalf("rekey must not change the record version: %d", got.Version)
		}
	}
	// Idempotent.
	if rep, err := rotated.Rekey(ctx); err != nil || rep.Changed != 0 {
		t.Fatalf("second Rekey = %+v, %v", rep, err)
	}
}

func TestRekeyReportsUnreadableRecords(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credentials.json")
	k1 := newKey(t, "k1")
	st, _ := New(path, newSealer(t, k1))
	good := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	bad := credential.Key{ClusterID: "c2", Scope: credential.ScopeUser, OwnerID: "u1"}
	for _, k := range []credential.Key{good, bad} {
		if _, err := st.Put(ctx, credential.Record{Meta: credential.Meta{Key: k}, Secret: []byte("s")}, 0); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := st.load()
	d.Records[st.find(d, bad)].SecretEnc[len(d.Records[st.find(d, bad)].SecretEnc)-1] ^= 1
	_ = st.save(d)
	rotated, _ := New(path, newSealer(t, newKey(t, "k2"), k1))
	rep, err := rotated.Rekey(ctx)
	if !errors.Is(err, credential.ErrRekeyIncomplete) || len(rep.Failed) != 1 || rep.Failed[0] != bad || rep.Changed != 1 {
		t.Fatalf("Rekey = %+v, %v", rep, err)
	}
}
