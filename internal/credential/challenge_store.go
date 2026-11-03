// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// ErrChallengeNotFound is the store-level "no such sign-in request". The Service never exposes it:
// unknown, foreign, finished and expired requests all look the same to a caller (ErrChallengeExpired).
var ErrChallengeNotFound = errors.New("the sign-in request was not found")

// Challenge states.
const (
	ChallengeActive = "active" // waiting for an answer or a poll
	ChallengeBusy   = "busy"   // one caller is working on it (claimed); a crashed worker's claim times out
)

// ChallengeRecord is a pending sign-in request as the store keeps it. State is encrypted by the Service
// and opaque to the store; everything else is non-secret routing data.
type ChallengeRecord struct {
	ID          string
	ClusterID   string
	Scope       Scope
	OwnerID     string
	SessionID   string
	Status      string
	Attempts    int // wrong answers so far
	LockedUntil time.Time
	ExpiresAt   time.Time
	CreatedAt   time.Time
	Version     int64
	KeyID       string
	State       []byte
}

// ChallengeStore keeps pending requests so that one can be completed on another replica. Implementations:
// memory (this package) and PostgreSQL (internal/pgstore); both pass credtest.ChallengeStoreContract.
type ChallengeStore interface {
	// Create stores a new record with Version 1. The ID must not exist.
	Create(ctx context.Context, rec ChallengeRecord) error
	// Get returns the record or ErrChallengeNotFound.
	Get(ctx context.Context, id string) (ChallengeRecord, error)
	// Swap replaces the record if its stored Version equals expected (ErrVersionConflict otherwise,
	// ErrChallengeNotFound if it is gone) and returns it with Version expected+1. The ID, the
	// subject fields and CreatedAt are never changed. This compare-and-swap is what makes the completion
	// of a request happen exactly once.
	Swap(ctx context.Context, rec ChallengeRecord, expected int64) (ChallengeRecord, error)
	// Delete removes the record; a missing record is not an error.
	Delete(ctx context.Context, id string) error
	// DeleteBy removes the records matching the filter (empty fields match anything).
	DeleteBy(ctx context.Context, f RevokeFilter) (int, error)
	// DeleteExpired removes the records that expired before the given time.
	DeleteExpired(ctx context.Context, before time.Time) (int, error)
}

// MemoryChallengeStore is an in-process ChallengeStore (desktop, single-instance web without PostgreSQL, tests).
type MemoryChallengeStore struct {
	mu   sync.Mutex
	recs map[string]ChallengeRecord
}

var _ ChallengeStore = (*MemoryChallengeStore)(nil)

// NewMemoryChallengeStore returns an empty store.
func NewMemoryChallengeStore() *MemoryChallengeStore {
	return &MemoryChallengeStore{recs: map[string]ChallengeRecord{}}
}

func cloneRec(r ChallengeRecord) ChallengeRecord {
	r.State = slices.Clone(r.State)
	return r
}

// Create implements ChallengeStore.
func (m *MemoryChallengeStore) Create(_ context.Context, rec ChallengeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.recs[rec.ID]; ok {
		return ErrVersionConflict
	}
	rec.Version = 1
	m.recs[rec.ID] = cloneRec(rec)
	return nil
}

// Get implements ChallengeStore.
func (m *MemoryChallengeStore) Get(_ context.Context, id string) (ChallengeRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok {
		return ChallengeRecord{}, ErrChallengeNotFound
	}
	return cloneRec(r), nil
}

// Swap implements ChallengeStore.
func (m *MemoryChallengeStore) Swap(_ context.Context, rec ChallengeRecord, expected int64) (ChallengeRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.recs[rec.ID]
	if !ok {
		return ChallengeRecord{}, ErrChallengeNotFound
	}
	if cur.Version != expected {
		return ChallengeRecord{}, ErrVersionConflict
	}
	rec.ClusterID, rec.Scope, rec.OwnerID, rec.SessionID, rec.CreatedAt = cur.ClusterID, cur.Scope, cur.OwnerID, cur.SessionID, cur.CreatedAt
	rec.Version = expected + 1
	m.recs[rec.ID] = cloneRec(rec)
	return cloneRec(rec), nil
}

// Delete implements ChallengeStore.
func (m *MemoryChallengeStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.recs, id)
	return nil
}

// DeleteBy implements ChallengeStore.
func (m *MemoryChallengeStore) DeleteBy(_ context.Context, f RevokeFilter) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.recs {
		if (f.ClusterID == "" || r.ClusterID == f.ClusterID) && (f.OwnerID == "" || r.OwnerID == f.OwnerID) &&
			(f.Scope == "" || r.Scope == f.Scope) {
			delete(m.recs, id)
			n++
		}
	}
	return n, nil
}

// DeleteExpired implements ChallengeStore.
func (m *MemoryChallengeStore) DeleteExpired(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.recs {
		if r.ExpiresAt.Before(before) {
			delete(m.recs, id)
			n++
		}
	}
	return n, nil
}

// Dump returns every stored byte (state blobs and ids), for "no plaintext at rest" tests.
func (m *MemoryChallengeStore) Dump() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []byte
	for _, r := range m.recs {
		out = append(out, r.ID...)
		out = append(out, r.ClusterID...)
		out = append(out, r.OwnerID...)
		out = append(out, r.SessionID...)
		out = append(out, r.KeyID...)
		out = append(out, r.State...)
	}
	return out
}
