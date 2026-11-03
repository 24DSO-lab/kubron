// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/credential/credtest"
	"github.com/24DSO-labs/kubron/internal/secretbox"
)

var testKubeconfig = []byte("apiVersion: v1\nkind: Config\n")

func ensureCluster(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.Clusters().Save(&Cluster{ID: id, DisplayName: "name-" + id, ContextName: "ctx"}, testKubeconfig); err != nil {
		t.Fatal(err)
	}
}

func openSecond(t *testing.T, keys *secretbox.Keyring) *Store {
	t.Helper()
	s, err := Open(context.Background(), os.Getenv("KUBRON_TEST_DATABASE_URL"), keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestCredentialStoreContract(t *testing.T) {
	credtest.StoreContract(t, func(t *testing.T) credtest.StoreHarness {
		s := newTestStore(t, keyString("k1"))
		ctx := context.Background()
		return credtest.StoreHarness{
			Store:   s.Credentials(),
			Prepare: func(t *testing.T, id string) { ensureCluster(t, s, id) },
			Second: func(t *testing.T) credential.CredentialStore {
				return openSecond(t, s.keys).Credentials()
			},
			Dump: func(t *testing.T) []byte {
				rows, err := s.pool.Query(ctx, `SELECT cluster_id, owner_kind, owner_id, kind, secret_enc, secret_key_id, enc_format, principal FROM cluster_credentials`)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var buf bytes.Buffer
				for rows.Next() {
					var c, o, oid, kind, kid, f, p string
					var enc []byte
					if err := rows.Scan(&c, &o, &oid, &kind, &enc, &kid, &f, &p); err != nil {
						t.Fatal(err)
					}
					fmt.Fprintf(&buf, "%s|%s|%s|%s|%s|%s|%s|", c, o, oid, kind, kid, f, p)
					buf.Write(enc)
					buf.WriteByte('\n')
				}
				return buf.Bytes()
			},
			RawBlob: func(t *testing.T, k credential.Key) ([]byte, string) {
				var blob []byte
				var kid string
				if err := s.pool.QueryRow(ctx, `SELECT secret_enc, secret_key_id FROM cluster_credentials WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3`,
					k.ClusterID, string(k.Scope), k.OwnerID).Scan(&blob, &kid); err != nil {
					t.Fatal(err)
				}
				return blob, kid
			},
			SetRawBlob: func(t *testing.T, k credential.Key, blob []byte, keyID string) {
				if _, err := s.pool.Exec(ctx, `UPDATE cluster_credentials SET secret_enc=$4, secret_key_id=$5 WHERE cluster_id=$1 AND owner_kind=$2 AND owner_id=$3`,
					k.ClusterID, string(k.Scope), k.OwnerID, blob, keyID); err != nil {
					t.Fatal(err)
				}
			},
		}
	})
}

func TestCredentialsMissingClusterIsNotFound(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	k := credential.Key{ClusterID: "nocluster", Scope: credential.ScopeUser, OwnerID: "u1"}
	_, err := s.Credentials().Put(context.Background(), credential.Record{Meta: credential.Meta{Key: k, Kind: credential.KindOIDC}, Secret: []byte("x")}, 0)
	if !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCredentialsDieWithTheCluster(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")
	k := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	if _, err := s.Credentials().Put(ctx, credential.Record{Meta: credential.Meta{Key: k, Kind: credential.KindOIDC}, Secret: []byte("x")}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Clusters().Delete("c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Credentials().Get(ctx, k); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("after cluster delete: %v", err)
	}
}

const oidcPlain = `{"issuerUrl":"https://idp.example","clientId":"kubron","clientSecret":"` + credtest.Canary + `-cs","scopes":["openid"],"username":"alice","password":"` + credtest.Canary + `-pw"}`

func saveOIDCCluster(t *testing.T, s *Store, id, plain string) {
	t.Helper()
	if err := s.Clusters().SaveWithSecret(&Cluster{ID: id, DisplayName: "name-" + id, ContextName: "ctx", Mode: "oidc-password"}, testKubeconfig, []byte(plain)); err != nil {
		t.Fatal(err)
	}
}

func legacyKey(s *Store, cluster string) credential.Key {
	return credential.Key{ClusterID: cluster, Scope: credential.ScopeTeam, OwnerID: s.teamID}
}

func TestMigrateLegacyOIDC(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	saveOIDCCluster(t, s, "o1", oidcPlain)
	saveOIDCCluster(t, s, "o2", oidcPlain)
	ensureCluster(t, s, "plain") // kubeconfig mode: must not be migrated

	res, err := s.MigrateLegacyOIDC(ctx)
	if err != nil || res != (LegacyMigration{Copied: 2}) {
		t.Fatalf("first run: %+v, %v", res, err)
	}

	// The ciphertext is copied byte for byte; the source is untouched.
	var src, dst []byte
	var srcKey, dstKey, format string
	if err := s.pool.QueryRow(ctx, `SELECT secret_enc, secret_key_id FROM clusters WHERE id='o1'`).Scan(&src, &srcKey); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT secret_enc, secret_key_id, enc_format FROM cluster_credentials WHERE cluster_id='o1'`).Scan(&dst, &dstKey, &format); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, dst) || srcKey != dstKey || format != credential.EncLegacyV1 {
		t.Fatalf("copy differs: bytes equal=%v key %q/%q format %q", bytes.Equal(src, dst), srcKey, dstKey, format)
	}
	var plainCount int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM cluster_credentials WHERE cluster_id='plain'`).Scan(&plainCount)
	if plainCount != 0 {
		t.Fatal("a kubeconfig cluster was migrated")
	}

	// It reads back as the exact 3.7 plaintext (format compatibility with the OIDC provider).
	got, err := s.Credentials().Get(ctx, legacyKey(s, "o1"))
	if err != nil || string(got.Secret) != oidcPlain || got.Kind != credential.KindOIDC || got.Version != 1 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if n, _ := s.CountLegacyCredentials(ctx); n != 2 {
		t.Fatalf("legacy count = %d", n)
	}

	// Idempotent.
	if res, err := s.MigrateLegacyOIDC(ctx); err != nil || res != (LegacyMigration{Skipped: 2}) {
		t.Fatalf("second run: %+v, %v", res, err)
	}

	// The old code keeps writing until the cut-over: a changed source is picked up.
	saveOIDCCluster(t, s, "o1", `{"username":"alice","password":"new"}`)
	if res, err := s.MigrateLegacyOIDC(ctx); err != nil || res != (LegacyMigration{Updated: 1, Skipped: 1}) {
		t.Fatalf("third run: %+v, %v", res, err)
	}
	got, _ = s.Credentials().Get(ctx, legacyKey(s, "o1"))
	if string(got.Secret) != `{"username":"alice","password":"new"}` || got.Version != 2 {
		t.Fatalf("refreshed copy = %+v", got)
	}
}

func TestMigrateLegacyOIDCNeverOverwritesConvertedRows(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	saveOIDCCluster(t, s, "o1", oidcPlain)
	if _, err := s.MigrateLegacyOIDC(ctx); err != nil {
		t.Fatal(err)
	}
	// The application writes the record in the envelope format (e.g. a refreshed token).
	cur, _ := s.Credentials().Get(ctx, legacyKey(s, "o1"))
	cur.Secret = []byte(`{"refresh":"R2"}`)
	if _, err := s.Credentials().Put(ctx, cur, cur.Version); err != nil {
		t.Fatal(err)
	}
	// The source changes afterwards (old code): the converted row must not be touched.
	saveOIDCCluster(t, s, "o1", `{"password":"stale"}`)
	if res, err := s.MigrateLegacyOIDC(ctx); err != nil || res != (LegacyMigration{Skipped: 1}) {
		t.Fatalf("run: %+v, %v", res, err)
	}
	got, _ := s.Credentials().Get(ctx, legacyKey(s, "o1"))
	if string(got.Secret) != `{"refresh":"R2"}` || got.EncFormat != credential.EncEnvelopeV1 {
		t.Fatalf("converted row was overwritten: %+v", got)
	}
}

// A legacy ciphertext moved to another cluster must fail: the AAD is checked strictly.
func TestLegacyRecordCannotBeMovedBetweenClusters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	saveOIDCCluster(t, s, "o1", oidcPlain)
	saveOIDCCluster(t, s, "o2", `{"password":"other"}`)
	if _, err := s.MigrateLegacyOIDC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE cluster_credentials SET secret_enc=(SELECT secret_enc FROM cluster_credentials WHERE cluster_id='o1'),
		secret_key_id=(SELECT secret_key_id FROM cluster_credentials WHERE cluster_id='o1') WHERE cluster_id='o2'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.Credentials().Get(ctx, legacyKey(s, "o2"))
	if err == nil || got.Secret != nil {
		t.Fatalf("a moved legacy record was read: %+v, %v", got, err)
	}
}

func TestLegacyFormatIsTeamOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")
	_, err := s.pool.Exec(ctx, `INSERT INTO cluster_credentials (cluster_id, owner_kind, owner_id, kind, secret_enc, secret_key_id, enc_format)
		VALUES ('c1','user','u1','oidc','\x00','k1','legacy-v1')`)
	if err == nil {
		t.Fatal("the database accepted a legacy-v1 row in the user scope")
	}
}

func TestRekeyConvertsLegacyAndRotatesKeys(t *testing.T) {
	ctx := context.Background()
	k1 := keyString("k1")
	s := newTestStore(t, k1)
	saveOIDCCluster(t, s, "o1", oidcPlain)
	ensureCluster(t, s, "c1")
	envKey := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	if _, err := s.Credentials().Put(ctx, credential.Record{Meta: credential.Meta{Key: envKey, Kind: credential.KindOIDC}, Secret: []byte("refresh-1")}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateLegacyOIDC(ctx); err != nil {
		t.Fatal(err)
	}

	team := s.teamID
	// Rotate the master key: the new key is current, the old one is still in the ring.
	n := openSecond(t, mustKeyring(t, keyString("k2"), k1))
	rep, err := n.Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Credentials != 2 || rep.LegacyCredentials != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if left, _ := n.CountLegacyCredentials(ctx); left != 0 {
		t.Fatalf("%d legacy records left", left)
	}
	var wrong int
	_ = n.pool.QueryRow(ctx, `SELECT count(*) FROM cluster_credentials WHERE secret_key_id <> 'k2' OR enc_format <> 'envelope-v1'`).Scan(&wrong)
	if wrong != 0 {
		t.Fatalf("%d records are not on the new key / format", wrong)
	}
	got, err := n.Credentials().Get(ctx, credential.Key{ClusterID: "o1", Scope: credential.ScopeTeam, OwnerID: team})
	if err != nil || string(got.Secret) != oidcPlain || got.Version != 1 {
		t.Fatalf("converted record = %+v, %v", got, err)
	}
	if got, err := n.Credentials().Get(ctx, envKey); err != nil || string(got.Secret) != "refresh-1" {
		t.Fatalf("user record = %+v, %v", got, err)
	}
	// Idempotent.
	if rep, err := n.Rekey(ctx); err != nil || rep.Credentials != 0 {
		t.Fatalf("second rekey = %+v, %v", rep, err)
	}
	// The legacy source column is not touched by the rekey of credentials (it has its own rekey path).
	var src int
	_ = n.pool.QueryRow(ctx, `SELECT count(*) FROM clusters WHERE secret_enc IS NOT NULL`).Scan(&src)
	if src != 1 {
		t.Fatalf("clusters.secret_enc rows = %d", src)
	}
}

func TestRekeyCredentialsReportsUnreadableRecords(t *testing.T) {
	ctx := context.Background()
	k1 := keyString("k1")
	s := newTestStore(t, k1)
	saveOIDCCluster(t, s, "o1", oidcPlain)
	if _, err := s.MigrateLegacyOIDC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE cluster_credentials SET secret_enc = set_byte(secret_enc, length(secret_enc)-1, get_byte(secret_enc, length(secret_enc)-1) # 1)`); err != nil {
		t.Fatal(err)
	}
	n := openSecond(t, mustKeyring(t, keyString("k2"), k1))
	rep, err := n.Credentials().RekeyCredentials(ctx)
	if !errors.Is(err, credential.ErrRekeyIncomplete) || len(rep.Failed) != 1 || rep.LegacyRemaining != 1 {
		t.Fatalf("report = %+v, err = %v", rep, err)
	}
	if left, _ := n.CountLegacyCredentials(ctx); left != 1 {
		t.Fatalf("legacy left = %d", left)
	}
}

// Two instances (replicas) on one database: a refresh-token rotation done by one is never lost by the other.
func TestTwoReplicasDoNotLoseTokenRotation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, keyString("k1"))
	ensureCluster(t, s, "c1")
	other := openSecond(t, s.keys)
	k := credential.Key{ClusterID: "c1", Scope: credential.ScopeUser, OwnerID: "u1"}
	if _, err := s.Credentials().Put(ctx, credential.Record{Meta: credential.Meta{Key: k, Kind: credential.KindOIDC}, Secret: []byte("R0")}, 0); err != nil {
		t.Fatal(err)
	}
	// Both replicas read version 1 and try to store their own rotation: exactly one wins, the loser sees
	// the winner's value when it re-reads (the Broker then uses it instead of refreshing again).
	a, _ := s.Credentials().Get(ctx, k)
	b, _ := other.Credentials().Get(ctx, k)
	a.Secret, b.Secret = []byte("R1-from-A"), []byte("R1-from-B")
	_, errA := s.Credentials().Put(ctx, a, a.Version)
	_, errB := other.Credentials().Put(ctx, b, b.Version)
	if (errA == nil) == (errB == nil) {
		t.Fatalf("exactly one writer must win: %v / %v", errA, errB)
	}
	loser := errA
	if errA == nil {
		loser = errB
	}
	if !errors.Is(loser, credential.ErrVersionConflict) {
		t.Fatalf("loser error = %v", loser)
	}
	got, _ := s.Credentials().Get(ctx, k)
	want := "R1-from-A"
	if errA != nil {
		want = "R1-from-B"
	}
	if string(got.Secret) != want || got.Version != 2 {
		t.Fatalf("stored = %q v%d, want %q v2", got.Secret, got.Version, want)
	}
}
