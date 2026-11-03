// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// New hashes use an explicit, versioned Argon2id encoding:
// argon2id$v=19$m=<KiB>,t=<iterations>,p=<parallelism>$<salt>$<key>.
// PBKDF2 hashes remain readable for a transparent migration on successful login.
const (
	hashScheme       = "argon2id"
	legacyHashScheme = "pbkdf2-sha256"
	saltLength       = 16
	keyLength        = 32
	minPassRunes     = 8
	maxPassBytes     = 256

	maxPBKDF2Iter = 10_000_000
	maxArgonMemory = 64 * 1024 // KiB; reject hostile parameters before allocating memory.
	maxArgonTime = 10
	maxArgonThreads = 16
)

var (
	// OWASP baseline: 19 MiB, 2 iterations, one lane.
	argon2Memory = uint32(19 * 1024) // KiB; variable so tests can use cheaper parameters.
	argon2Time = uint32(2)
	argon2Threads = uint8(1)
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)
)

// ValidateUsername проверяет формат логина.
func ValidateUsername(username string) error {
	if !usernameRe.MatchString(username) {
		return errors.New("username must be 3–32 characters: Latin letters, digits, dot, hyphen and underscore")
	}
	return nil
}

// ValidatePassword проверяет требования к паролю.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPassRunes {
		return fmt.Errorf("password must be at least %d characters", minPassRunes)
	}
	if len(password) > maxPassBytes {
		return fmt.Errorf("password is too long (maximum %d bytes)", maxPassBytes)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to get random data: %w", err)
	}
	key := argon2.IDKey(passwordBytes(password), salt, argon2Time, argon2Memory, argon2Threads, keyLength)
	return fmt.Sprintf("%s$v=19$m=%d,t=%d,p=%d$%s$%s",
		hashScheme, argon2Memory, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func passwordBytes(password string) []byte {
	return []byte(password)
}

// verifyPassword uses constant-time comparison and reports whether a successful login
// should upgrade the stored hash to the current Argon2id parameters.
func verifyPassword(password, encoded string) (ok, needsRehash bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) == 4 && parts[0] == legacyHashScheme {
		return verifyPBKDF2(password, parts)
	}
	if len(parts) != 5 || parts[0] != hashScheme || parts[1] != "v=19" {
		return false, false
	}

	var memory, iterations uint32
	var threads uint8
	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return false, false
	}
	for _, param := range params {
		kv := strings.SplitN(param, "=", 2)
		if len(kv) != 2 {
			return false, false
		}
		switch kv[0] {
		case "m":
			n, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil {
				return false, false
			}
			memory = uint32(n)
		case "t":
			n, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil { return false, false }
			iterations = uint32(n)
		case "p":
			n, err := strconv.ParseUint(kv[1], 10, 8)
			if err != nil { return false, false }
			threads = uint8(n)
		default:
			return false, false
		}
	}
	if memory < 8*uint32(threads) || memory > maxArgonMemory ||
		iterations < 1 || iterations > maxArgonTime ||
		threads < 1 || threads > maxArgonThreads {
		return false, false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, false
	}
	got := argon2.IDKey(passwordBytes(password), salt, iterations, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false
	}
	return true, memory != argon2Memory || iterations != argon2Time ||
		threads != argon2Threads || len(want) != keyLength
}

func verifyPBKDF2(password string, parts []string) (ok, needsRehash bool) {
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > maxPBKDF2Iter {
		return false, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil || subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false
	}
	return true, true
}
