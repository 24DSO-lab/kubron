// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/secretbox"
)

const canary = "CANARY-s3cr3t-refresh-token-9f2a"

func mustKey(t *testing.T, id string) secretbox.Key {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k.ID = id
	return k
}

func mustRing(t *testing.T, cur secretbox.Key, prev ...secretbox.Key) *secretbox.Keyring {
	t.Helper()
	kr, err := secretbox.NewKeyring(cur, prev...)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func newEnv(t *testing.T, id string) *Envelope {
	t.Helper()
	e, err := NewEnvelope(mustRing(t, mustKey(t, id)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEnvelopeRoundTrip(t *testing.T) {
	e := newEnv(t, "k1")
	aad := AAD("c1", "user", "u1")
	blob, keyID, err := e.Seal([]byte(canary), aad)
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "k1" || !IsEnvelope(blob) {
		t.Fatalf("keyID=%q envelope=%v", keyID, IsEnvelope(blob))
	}
	if bytes.Contains(blob, []byte(canary)) {
		t.Fatal("plaintext is visible in the record")
	}
	got, err := e.Open(blob, aad)
	if err != nil || string(got) != canary {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if id, err := KeyID(blob); err != nil || id != "k1" {
		t.Fatalf("KeyID = %q, %v", id, err)
	}
}

func TestEnvelopeEmptyPlaintext(t *testing.T) {
	e := newEnv(t, "k1")
	aad := AAD("c1", "team", "t1")
	blob, _, err := e.Seal(nil, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Open(blob, aad)
	if err != nil || len(got) != 0 {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestEnvelopeUniqueDEKAndNonce(t *testing.T) {
	e := newEnv(t, "k1")
	aad := AAD("c1", "user", "u1")
	a, _, _ := e.Seal([]byte("same"), aad)
	b, _, _ := e.Seal([]byte("same"), aad)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical")
	}
}

func TestEnvelopeAADBinding(t *testing.T) {
	e := newEnv(t, "k1")
	blob, _, err := e.Seal([]byte(canary), AAD("c1", "user", "u1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, aad := range [][]byte{
		AAD("c2", "user", "u1"), // another cluster
		AAD("c1", "team", "u1"), // another scope
		AAD("c1", "user", "u2"), // another owner
		nil,
	} {
		if _, err := e.Open(blob, aad); !errors.Is(err, secretbox.ErrDecrypt) {
			t.Fatalf("aad %q: err = %v, want ErrDecrypt", aad, err)
		}
	}
}

// Corrupted data must be an error, never an empty value.
func TestEnvelopeCorruptionIsAnError(t *testing.T) {
	e := newEnv(t, "k1")
	aad := AAD("c1", "user", "u1")
	blob, _, err := e.Seal([]byte(canary), aad)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(envelopeMagic); i < len(blob); i++ {
		bad := bytes.Clone(blob)
		bad[i] ^= 0x01
		got, err := e.Open(bad, aad)
		if err == nil {
			t.Fatalf("flip at byte %d: no error, got %q", i, got)
		}
		if got != nil {
			t.Fatalf("flip at byte %d: plaintext returned together with an error", i)
		}
	}
	for n := len(envelopeMagic); n < len(blob); n++ {
		if got, err := e.Open(blob[:n], aad); err == nil || got != nil {
			t.Fatalf("truncated to %d: err=%v got=%q", n, err, got)
		}
	}
}

func TestEnvelopeNotEnvelope(t *testing.T) {
	e := newEnv(t, "k1")
	for _, b := range [][]byte{nil, {}, []byte("KB"), []byte("not an envelope at all")} {
		if _, err := e.Open(b, nil); !errors.Is(err, ErrNotEnvelope) {
			t.Fatalf("Open(%q) = %v, want ErrNotEnvelope", b, err)
		}
	}
}

func TestEnvelopeUnknownKey(t *testing.T) {
	a := newEnv(t, "k1")
	b := newEnv(t, "k2")
	blob, _, _ := a.Seal([]byte(canary), nil)
	if _, err := b.Open(blob, nil); !errors.Is(err, secretbox.ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
}

func TestEnvelopeSameIDWrongBytes(t *testing.T) {
	a := newEnv(t, "k1")
	b := newEnv(t, "k1") // same id, other key material
	blob, _, _ := a.Seal([]byte(canary), nil)
	if _, err := b.Open(blob, nil); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestEnvelopeRotationAndRewrap(t *testing.T) {
	oldKey, newKey := mustKey(t, "old"), mustKey(t, "new")
	oldEnv, _ := NewEnvelope(mustRing(t, oldKey))
	aad := AAD("c1", "user", "u1")
	blob, _, err := oldEnv.Seal([]byte(canary), aad)
	if err != nil {
		t.Fatal(err)
	}

	rotated, _ := NewEnvelope(mustRing(t, newKey, oldKey))
	if !rotated.NeedsRotation(blob) {
		t.Fatal("record under the old key must need rotation")
	}
	if got, err := rotated.Open(blob, aad); err != nil || string(got) != canary {
		t.Fatalf("old record is unreadable after rotation: %q, %v", got, err)
	}
	out, id, changed, err := rotated.Rewrap(blob, aad)
	if err != nil || !changed || id != "new" {
		t.Fatalf("Rewrap = id %q changed %v err %v", id, changed, err)
	}
	if rotated.NeedsRotation(out) {
		t.Fatal("rewrapped record still needs rotation")
	}
	// The record is now readable without the previous key.
	onlyNew, _ := NewEnvelope(mustRing(t, newKey))
	if got, err := onlyNew.Open(out, aad); err != nil || string(got) != canary {
		t.Fatalf("Open after rewrap = %q, %v", got, err)
	}
	// Second rewrap is a no-op.
	if _, _, changed, err := rotated.Rewrap(out, aad); err != nil || changed {
		t.Fatalf("second Rewrap changed=%v err=%v", changed, err)
	}
	// Rewrap must refuse a record that does not belong to aad.
	if _, _, _, err := rotated.Rewrap(blob, AAD("c9", "user", "u1")); err == nil {
		t.Fatal("Rewrap accepted a record bound to another aad")
	}
}

func TestEnvelopeErrorsDoNotLeak(t *testing.T) {
	e := newEnv(t, "k1")
	blob, _, _ := e.Seal([]byte(canary), AAD("c1", "user", "u1"))
	bad := bytes.Clone(blob)
	bad[len(bad)-1] ^= 1
	_, err := e.Open(bad, AAD("c1", "user", "u1"))
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestNewEnvelopeNil(t *testing.T) {
	if _, err := NewEnvelope(nil); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("err = %v", err)
	}
}

func FuzzEnvelopeOpen(f *testing.F) {
	k, _ := GenerateKey()
	kr, _ := secretbox.NewKeyring(k)
	e, _ := NewEnvelope(kr)
	blob, _, _ := e.Seal([]byte("x"), []byte("aad"))
	f.Add(blob)
	f.Add([]byte("KBE\x01"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := e.Open(in, []byte("aad"))
		if err != nil && got != nil {
			t.Fatal("plaintext together with an error")
		}
		_, _, _, _ = e.Rewrap(in, []byte("aad"))
		_, _ = KeyID(in)
	})
}
