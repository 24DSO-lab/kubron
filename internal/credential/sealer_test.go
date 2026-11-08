// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/secretbox"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

const legacyPlain = `{"issuerUrl":"https://idp.example","clientId":"kubron","clientSecret":"CANARY-cs","scopes":["openid"],"username":"alice","password":"CANARY-pw"}`

func testKeyring(t *testing.T, id string, prev ...secretbox.Key) (*secretbox.Keyring, secretbox.Key) {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k.ID = id
	kr, err := secretbox.NewKeyring(k, prev...)
	if err != nil {
		t.Fatal(err)
	}
	return kr, k
}

func testSealer(t *testing.T, kr *secretbox.Keyring) *Sealer {
	t.Helper()
	env, err := secrets.NewEnvelope(kr)
	if err != nil {
		t.Fatal(err)
	}
	return NewSealer(env)
}

// A 3.7 value as the old code wrote it: secretbox with the AAD cluster/<id>/secret.
func legacyBlob(t *testing.T, kr *secretbox.Keyring, cluster string) ([]byte, string) {
	t.Helper()
	blob, id, err := kr.Seal([]byte(legacyPlain), LegacyTeamAAD(cluster))
	if err != nil {
		t.Fatal(err)
	}
	return blob, id
}

func TestSealerLegacyToEnvelopeKeepsPlaintext(t *testing.T) {
	kr, _ := testKeyring(t, "k1")
	s := testSealer(t, kr)
	k := Key{ClusterID: "c1", Scope: ScopeTeam, OwnerID: "t1"}
	blob, keyID := legacyBlob(t, kr, "c1")

	got, err := s.Open(k, blob, keyID, EncLegacyV1)
	if err != nil || string(got) != legacyPlain {
		t.Fatalf("legacy Open = %q, %v", got, err)
	}
	out, id, changed, err := s.Rewrap(k, blob, keyID, EncLegacyV1)
	if err != nil || !changed || id != "k1" {
		t.Fatalf("Rewrap = %v %q %v", changed, id, err)
	}
	// The plaintext format of task 3.7 is byte for byte the same after the conversion.
	got, err = s.Open(k, out, id, EncEnvelopeV1)
	if err != nil || string(got) != legacyPlain {
		t.Fatalf("envelope Open = %q, %v", got, err)
	}
}

func TestSealerLegacyIsStrict(t *testing.T) {
	kr, _ := testKeyring(t, "k1")
	s := testSealer(t, kr)
	blob, keyID := legacyBlob(t, kr, "c1")

	// A record moved to another cluster is rejected.
	if _, err := s.Open(Key{ClusterID: "c2", Scope: ScopeTeam, OwnerID: "t1"}, blob, keyID, EncLegacyV1); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("moved legacy record: %v, want ErrDecrypt", err)
	}
	// Legacy is for the team scope only.
	if _, err := s.Open(Key{ClusterID: "c1", Scope: ScopeUser, OwnerID: "u1"}, blob, keyID, EncLegacyV1); !errors.Is(err, ErrLegacyNotAllowed) {
		t.Fatalf("legacy in the user scope: %v, want ErrLegacyNotAllowed", err)
	}
	// The format is stated, not guessed: an envelope blob labelled legacy does not open, and vice versa.
	k := Key{ClusterID: "c1", Scope: ScopeTeam, OwnerID: "t1"}
	env, id, _ := s.Seal(k, []byte("x"))
	if _, err := s.Open(k, env, id, EncLegacyV1); err == nil {
		t.Fatal("an envelope blob opened as legacy")
	}
	if _, err := s.Open(k, blob, keyID, EncEnvelopeV1); err == nil {
		t.Fatal("a legacy blob opened as envelope")
	}
	if _, err := s.Open(k, blob, keyID, ""); !errors.Is(err, ErrUnknownEncFormat) {
		t.Fatalf("empty format: %v", err)
	}
	// Corruption is an error, not an empty value.
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 1
	if got, err := s.Open(k, bad, keyID, EncLegacyV1); err == nil || got != nil {
		t.Fatalf("corrupted legacy blob: %q, %v", got, err)
	}
}

func TestSealerNeverWritesLegacy(t *testing.T) {
	kr, _ := testKeyring(t, "k1")
	s := testSealer(t, kr)
	k := Key{ClusterID: "c1", Scope: ScopeTeam, OwnerID: "t1"}
	blob, _, err := s.Seal(k, []byte("x"))
	if err != nil || !secrets.IsEnvelope(blob) {
		t.Fatalf("Seal: %v envelope=%v", err, secrets.IsEnvelope(blob))
	}
}

func TestSealerRewrapAfterKeyRotation(t *testing.T) {
	oldKr, oldKey := testKeyring(t, "old")
	blob, keyID := legacyBlob(t, oldKr, "c1")
	newKr, _ := testKeyring(t, "new", oldKey)
	s := testSealer(t, newKr)
	k := Key{ClusterID: "c1", Scope: ScopeTeam, OwnerID: "t1"}
	out, id, changed, err := s.Rewrap(k, blob, keyID, EncLegacyV1)
	if err != nil || !changed || id != "new" {
		t.Fatalf("Rewrap = %q %v %v", id, changed, err)
	}
	if got, err := s.Open(k, out, id, EncEnvelopeV1); err != nil || string(got) != legacyPlain {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestKeyValidate(t *testing.T) {
	good := Key{ClusterID: "c-1_x", Scope: ScopeUser, OwnerID: "u/1"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Key{ClusterID: "a/b", Scope: ScopeUser, OwnerID: "u"}).Validate(); !errors.Is(err, ErrInvalidKey) {
		t.Fatal("slash in cluster id accepted")
	}
}
