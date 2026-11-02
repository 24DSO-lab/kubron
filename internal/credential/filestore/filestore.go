// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package filestore is the file implementation of credential.CredentialStore for the desktop and for a
// server without PostgreSQL. Records are kept in one JSON file (mode 0600) with encrypted secrets; every
// write replaces the file atomically.
//
// Writers are serialized per file path inside the process and across processes with a lock file, so two
// Store values over the same path never lose an update.
package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	formatVersion = 1
	lockTimeout   = 5 * time.Second
	lockStale     = 30 * time.Second
)

type doc struct {
	Version int   `json:"version"`
	Records []rec `json:"records"`
}

type rec struct {
	ClusterID string     `json:"clusterId"`
	Scope     string     `json:"scope"`
	OwnerID   string     `json:"ownerId"`
	Kind      string     `json:"kind"`
	Principal string     `json:"principal,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Version   int64      `json:"version"`
	UpdatedAt time.Time  `json:"updatedAt"`
	KeyID     string     `json:"keyId"`
	EncFormat string     `json:"encFormat"`
	SecretEnc []byte     `json:"secretEnc"`
}

func (r rec) key() credential.Key {
	return credential.Key{ClusterID: r.ClusterID, Scope: credential.Scope(r.Scope), OwnerID: r.OwnerID}
}

func (r rec) meta() credential.Meta {
	return credential.Meta{Key: r.key(), Kind: credential.Kind(r.Kind), Principal: r.Principal,
		ExpiresAt: r.ExpiresAt, Version: r.Version, UpdatedAt: r.UpdatedAt, KeyID: r.KeyID, EncFormat: r.EncFormat}
}

var pathLocks sync.Map // absolute path -> *sync.Mutex

// Store implements credential.CredentialStore on a file.
type Store struct {
	path   string
	sealer *credential.Sealer
	now    func() time.Time
}

var _ credential.CredentialStore = (*Store)(nil)

// New returns a Store that keeps its data in path. The directory is created with mode 0700.
func New(path string, sealer *credential.Sealer) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials file path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("create credentials directory: %w", err)
	}
	return &Store{path: abs, sealer: sealer, now: time.Now}, nil
}

func (s *Store) load() (doc, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return doc{Version: formatVersion}, nil
	}
	if err != nil {
		return doc{}, fmt.Errorf("read credentials file: %w", err)
	}
	var d doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return doc{}, errors.New("the credentials file is damaged")
	}
	if d.Version != formatVersion {
		return doc{}, errors.New("the credentials file has an unsupported format version")
	}
	return d, nil
}

func (s *Store) save(d doc) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil && !isWindowsChmod(err) {
		_ = tmp.Close()
		return fmt.Errorf("write credentials file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credentials file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	ok = true
	if d, err := os.Open(filepath.Dir(s.path)); err == nil { // make the rename durable (best effort)
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func isWindowsChmod(error) bool { return os.PathSeparator == '\\' }

// lock serializes writers of the same file: a mutex inside the process and a lock file between processes.
func (s *Store) lock(ctx context.Context) (func(), error) {
	mu, _ := pathLocks.LoadOrStore(s.path, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	lockPath := s.path + ".lock"
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath); m.Unlock() }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			m.Unlock()
			return nil, fmt.Errorf("lock credentials file: %w", err)
		}
		if fi, serr := os.Stat(lockPath); serr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(lockPath) // left behind by a crashed process
			continue
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			m.Unlock()
			return nil, errors.New("the credentials file is locked by another process")
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Store) find(d doc, k credential.Key) int {
	return slices.IndexFunc(d.Records, func(r rec) bool { return r.key() == k })
}

// Get implements credential.CredentialStore.
func (s *Store) Get(_ context.Context, k credential.Key) (credential.Record, error) {
	if err := k.Validate(); err != nil {
		return credential.Record{}, err
	}
	d, err := s.load()
	if err != nil {
		return credential.Record{}, err
	}
	i := s.find(d, k)
	if i < 0 {
		return credential.Record{}, credential.ErrNotFound
	}
	plain, err := s.sealer.Open(k, d.Records[i].SecretEnc, d.Records[i].KeyID, d.Records[i].EncFormat)
	if err != nil {
		return credential.Record{}, err
	}
	return credential.Record{Meta: d.Records[i].meta(), Secret: plain}, nil
}

// Put implements credential.CredentialStore.
func (s *Store) Put(ctx context.Context, r credential.Record, expected int64) (credential.Meta, error) {
	if err := r.Key.Validate(); err != nil {
		return credential.Meta{}, err
	}
	blob, keyID, err := s.sealer.Seal(r.Key, r.Secret)
	if err != nil {
		return credential.Meta{}, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return credential.Meta{}, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return credential.Meta{}, err
	}
	i := s.find(d, r.Key)
	next := rec{ClusterID: r.ClusterID, Scope: string(r.Scope), OwnerID: r.OwnerID, Kind: string(r.Kind),
		Principal: r.Principal, ExpiresAt: r.ExpiresAt, KeyID: keyID, EncFormat: credential.EncEnvelopeV1, SecretEnc: blob,
		UpdatedAt: s.now().UTC().Truncate(time.Microsecond)}
	switch {
	case i < 0 && (expected == 0 || expected == credential.AnyVersion):
		next.Version = 1
		d.Records = append(d.Records, next)
	case i >= 0 && (expected == credential.AnyVersion || expected == d.Records[i].Version):
		next.Version = d.Records[i].Version + 1
		d.Records[i] = next
	default:
		return credential.Meta{}, credential.ErrVersionConflict
	}
	if err := s.save(d); err != nil {
		return credential.Meta{}, err
	}
	return next.meta(), nil
}

// Delete implements credential.CredentialStore.
func (s *Store) Delete(ctx context.Context, k credential.Key) error {
	if err := k.Validate(); err != nil {
		return err
	}
	_, err := s.revoke(ctx, func(r rec) bool { return r.key() == k })
	return err
}

// List implements credential.CredentialStore.
func (s *Store) List(_ context.Context, ownerID string) ([]credential.Meta, error) {
	d, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []credential.Meta
	for _, r := range d.Records {
		if r.OwnerID == ownerID {
			out = append(out, r.meta())
		}
	}
	slices.SortFunc(out, func(a, b credential.Meta) int {
		if c := strings.Compare(a.ClusterID, b.ClusterID); c != 0 {
			return c
		}
		return strings.Compare(string(a.Scope), string(b.Scope))
	})
	return out, nil
}

// Revoke implements credential.CredentialStore.
func (s *Store) Revoke(ctx context.Context, f credential.RevokeFilter) (int, error) {
	return s.revoke(ctx, func(r rec) bool {
		return (f.ClusterID == "" || r.ClusterID == f.ClusterID) &&
			(f.OwnerID == "" || r.OwnerID == f.OwnerID) &&
			(f.Scope == "" || r.Scope == string(f.Scope))
	})
}

func (s *Store) revoke(ctx context.Context, match func(rec) bool) (int, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return 0, err
	}
	before := len(d.Records)
	d.Records = slices.DeleteFunc(d.Records, match)
	n := before - len(d.Records)
	if n == 0 {
		return 0, nil
	}
	return n, s.save(d)
}

// Rekey re-encrypts every record with the current master key (and the envelope format). Records that
// cannot be read are left untouched and listed in the report; the error is then ErrRekeyIncomplete.
func (s *Store) Rekey(ctx context.Context) (credential.RekeyReport, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return credential.RekeyReport{}, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return credential.RekeyReport{}, err
	}
	rep := credential.RekeyReport{Total: len(d.Records)}
	for i, r := range d.Records {
		out, id, changed, err := s.sealer.Rewrap(r.key(), r.SecretEnc, r.KeyID, r.EncFormat)
		if err != nil {
			rep.Failed = append(rep.Failed, r.key())
			continue
		}
		if changed {
			d.Records[i].SecretEnc, d.Records[i].KeyID, d.Records[i].EncFormat = out, id, credential.EncEnvelopeV1
			rep.Changed++
		}
	}
	if rep.Changed > 0 {
		if err := s.save(d); err != nil {
			return rep, err
		}
	}
	if len(rep.Failed) > 0 {
		return rep, credential.ErrRekeyIncomplete
	}
	return rep, nil
}
