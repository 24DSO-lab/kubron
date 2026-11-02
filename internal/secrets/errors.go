// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import "errors"

var (
	// ErrNoMasterKey means that no master key is configured or stored.
	ErrNoMasterKey = errors.New("master encryption key is not configured")
	// ErrSecretKeyRequired means that a server with PostgreSQL has no KUBRON_SECRET_KEY.
	ErrSecretKeyRequired = errors.New("KUBRON_SECRET_KEY is required when PostgreSQL is used")
	// ErrKeyStoreUnavailable means that the master key storage cannot be used on this machine.
	ErrKeyStoreUnavailable = errors.New("the master key storage is unavailable")
	// ErrKeyExists is returned when a master key must not be overwritten.
	ErrKeyExists = errors.New("a master encryption key already exists")
	// ErrReadOnlyKeyStore is returned by key sources that cannot store a key (environment).
	ErrReadOnlyKeyStore = errors.New("this master key source is read-only")
	// ErrKeyFileInsecure means that the key file is readable by other users and cannot be tightened.
	ErrKeyFileInsecure = errors.New("the master key file is accessible to other users")
	// ErrNotEnvelope means that the data is not an envelope record (for example a legacy secretbox value).
	ErrNotEnvelope = errors.New("the data is not an envelope-encrypted record")
)
