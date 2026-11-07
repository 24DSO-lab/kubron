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
	mu       sync.Mutex
	recs     map[string]ChallengeRecord
	attempts map[string]attemptBucket
}

var _ ChallengeStore = (*MemoryChallengeStore)(nil)
var _ AttemptLimiter = (*MemoryChallengeStore)(nil)

// NewMemoryChallengeStore returns an empty store.
func NewMemoryChallengeStore() *MemoryChallengeStore {
	return &MemoryChallengeStore{recs: map[string]ChallengeRecord{}, attempts: map[string]attemptBucket{}}
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

// attemptBucket is one opaque user/IdP failure counter.
type attemptBucket struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	updatedAt   time.Time
}

func attemptBucketKey(dimension, key string) string { return dimension + ":" + key }

// Check implements AttemptLimiter.
func (m *MemoryChallengeStore) Check(_ context.Context, userKey, idpKey string, now time.Time) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var wait time.Duration
	for _, key := range []string{attemptBucketKey("user", userKey), attemptBucketKey("idp", idpKey)} {
		if key == "idp:" {
			continue
		}
		if b, ok := m.attempts[key]; ok && now.Before(b.lockedUntil) {
			wait = max(wait, b.lockedUntil.Sub(now))
		}
	}
	return wait, nil
}

// Fail implements AttemptLimiter. Both dimensions are updated under one lock.
func (m *MemoryChallengeStore) Fail(_ context.Context, userKey, idpKey string, now time.Time, window time.Duration, userLimit, idpLimit int, lockout time.Duration) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	increment := func(key string, limit int) time.Duration {
		if key == "" || limit <= 0 {
			return 0
		}
		bucketKey := key
		b := m.attempts[bucketKey]
		if now.Before(b.lockedUntil) {
			return b.lockedUntil.Sub(now)
		}
		if b.windowStart.IsZero() || !now.Before(b.windowStart.Add(window)) || !b.lockedUntil.IsZero() {
			b = attemptBucket{windowStart: now}
		}
		b.failures++
		b.updatedAt = now
		if b.failures >= limit {
			b.lockedUntil = now.Add(lockout)
		}
		m.attempts[bucketKey] = b
		if now.Before(b.lockedUntil) {
			return b.lockedUntil.Sub(now)
		}
		return 0
	}
	wait := increment(attemptBucketKey("user", userKey), userLimit)
	if idpKey != "" {
		wait = max(wait, increment(attemptBucketKey("idp", idpKey), idpLimit))
	}
	return wait, nil
}

// Reset implements AttemptLimiter. A successful user sign-in clears that user's counter; the aggregate IdP counter expires by window and is not cleared by another user's success.
func (m *MemoryChallengeStore) Reset(_ context.Context, userKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.attempts, attemptBucketKey("user", userKey))
	return nil
}

// Sweep implements AttemptLimiter and removes counters that are no longer useful.
func (m *MemoryChallengeStore) Sweep(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for key, b := range m.attempts {
		if b.updatedAt.Before(before) && !before.Before(b.lockedUntil) {
			delete(m.attempts, key)
			n++
		}
	}
	return n, nil
}
