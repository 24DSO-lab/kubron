// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package secrets implements envelope encryption for cluster sign-in data (ADR-0008, ADR-0018 section 6).
//
// Every record is encrypted with its own random data key (DEK, AES-256-GCM). The DEK is stored in the
// record, wrapped with the master key (KEK) that comes from a MasterKeyStore: the system keyring on
// desktop, the KUBRON_SECRET_KEY variable on a server, or a 0600 file as a fallback. Both layers are
// bound to the record with additional authenticated data, so a ciphertext cannot be moved between
// clusters, scopes or owners.
//
// The package depends on the standard library, internal/secretbox and the OS keyring library only.
// It never logs or returns key material.
package secrets
