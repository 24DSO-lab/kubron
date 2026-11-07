// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestArgon2idEncodingAndVerification(t *testing.T) {
	hash, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) == 0 || hash[:9] != "argon2id$" {
		t.Fatalf("new hash must use Argon2id, got %q", hash)
	}
	if ok, rehash := verifyPassword("correct horse", hash); !ok || rehash {
		t.Fatalf("fresh hash: ok=%v rehash=%v", ok, rehash)
	}
	if ok, _ := verifyPassword("wrong password", hash); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestLegacyPBKDF2IsVerifiedAndMarkedForUpgrade(t *testing.T) {
	const password = "legacy-password"
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, password, salt, 600_000, keyLength)
	if err != nil {
		t.Fatal(err)
	}
	encoded := fmt.Sprintf("%s$600000$%s$%s", legacyHashScheme,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
	if ok, rehash := verifyPassword(password, encoded); !ok || !rehash {
		t.Fatalf("legacy PBKDF2 must verify and request upgrade: ok=%v rehash=%v", ok, rehash)
	}
	if ok, _ := verifyPassword("wrong password", encoded); ok {
		t.Fatal("wrong password accepted for legacy hash")
	}
}

func TestArgon2idRejectsUntrustedParameters(t *testing.T) {
	cases := []string{
		"argon2id$v=19$m=1,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=19$m=999999999,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=19$m=19456,t=999,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=19$m=19456,t=2,p=0$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=16$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
	}
	for _, encoded := range cases {
		if ok, _ := verifyPassword("password", encoded); ok {
			t.Errorf("malformed or excessive hash parameters accepted: %s", encoded)
		}
	}
}

func TestLoginUpgradesLegacyPBKDF2Hash(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}

	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, goodPass, salt, 1000, keyLength)
	if err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("%s$1000$%s$%s", legacyHashScheme,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))

	s.mu.Lock()
	s.st.findByName("admin").PasswordHash = legacy
	s.mu.Unlock()

	if _, err := s.Login(t.Context(), "admin", goodPass); err != nil {
		t.Fatalf("legacy login failed: %v", err)
	}
	s.mu.Lock()
	stored := s.st.findByName("admin").PasswordHash
	s.mu.Unlock()
	if !strings.HasPrefix(stored, "argon2id$") {
		t.Fatalf("successful login did not upgrade the stored hash: %q", stored)
	}
}
