// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	gokeyring "github.com/zalando/go-keyring"

	"github.com/24DSO-lab/kubron/internal/secretbox"
)

// osKeyring is the part of the OS keyring API that the store needs (replaced in tests).
type osKeyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) { return gokeyring.Get(service, user) }
func (systemKeyring) Set(service, user, secret string) error {
	return gokeyring.Set(service, user, secret)
}
func (systemKeyring) Delete(service, user string) error { return gokeyring.Delete(service, user) }

// keyringTimeout bounds every call: on a headless Linux the Secret Service may hang waiting for a prompt.
const keyringTimeout = 5 * time.Second

// KeyringStore keeps the master key in the system keyring (Keychain, Credential Manager, Secret Service).
type KeyringStore struct {
	Service string
	User    string
	backend osKeyring
}

// NewKeyringStore returns a store backed by the system keyring.
func NewKeyringStore(service, user string) *KeyringStore {
	return &KeyringStore{Service: service, User: user, backend: systemKeyring{}}
}

func (*KeyringStore) Name() string { return "keyring" }

func (s *KeyringStore) be() osKeyring {
	if s.backend == nil {
		return systemKeyring{}
	}
	return s.backend
}

type keyringResult struct {
	val string
	err error
}

// call runs fn with a deadline; the keyring API itself has no context.
func call(ctx context.Context, fn func() (string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, keyringTimeout)
	defer cancel()
	ch := make(chan keyringResult, 1)
	go func() {
		v, err := fn()
		ch <- keyringResult{v, err}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("%w: %v", ErrKeyStoreUnavailable, ctx.Err())
	}
}

// Available probes the keyring with a read of a key that does not exist: "not found" proves that the
// backend works, anything else means that it does not.
func (s *KeyringStore) Available(ctx context.Context) error {
	_, err := call(ctx, func() (string, error) { return s.be().Get(s.Service, s.User+".probe") })
	if err == nil || errors.Is(err, gokeyring.ErrNotFound) {
		return nil
	}
	if errors.Is(err, ErrKeyStoreUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrKeyStoreUnavailable, err)
}

// Load reads and parses the stored key.
func (s *KeyringStore) Load(ctx context.Context) (*secretbox.Keyring, error) {
	v, err := call(ctx, func() (string, error) { return s.be().Get(s.Service, s.User) })
	if errors.Is(err, gokeyring.ErrNotFound) {
		return nil, ErrNoMasterKey
	}
	if err != nil {
		if errors.Is(err, ErrKeyStoreUnavailable) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrKeyStoreUnavailable, err)
	}
	return secretbox.ParseKeyring(v)
}

// Save stores the key unless one is already there.
func (s *KeyringStore) Save(ctx context.Context, key secretbox.Key) error {
	if _, err := s.Load(ctx); err == nil {
		return ErrKeyExists
	} else if !errors.Is(err, ErrNoMasterKey) {
		return err
	}
	_, err := call(ctx, func() (string, error) { return "", s.be().Set(s.Service, s.User, FormatKey(key)) })
	if err != nil {
		if errors.Is(err, ErrKeyStoreUnavailable) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrKeyStoreUnavailable, err)
	}
	return nil
}
