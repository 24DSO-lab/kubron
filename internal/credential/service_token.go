// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"errors"
	"time"
)

var (
	_ Broker      = (*Service)(nil)
	_ Invalidator = (*Service)(nil)
)

// Invalidator is an optional interface of a Broker implementation. The transport calls it when the API server
// answers 401, so that a cached token (above all one without expiry) is dropped and obtained again.
type Invalidator interface {
	Invalidate(k Key)
}

// maxRefreshRounds bounds the retries of one refresh when other replicas keep rotating the saved data.
const maxRefreshRounds = 4

// Token implements Broker: it returns a valid token without asking the user, or ErrChallengeRequired when
// that is impossible (nothing saved, or the saved sign-in is no longer valid); the caller then starts an
// interactive sign-in with Acquire.
//
// For KindStatic there is no token: the zero Token and a nil error mean "use the kubeconfig as it is".
//
// The token is cached in memory with a 30 s margin. Concurrent calls for one subject share a single refresh
// (single flight). Between replicas the saved data is guarded by its version: if another replica has already
// rotated the refresh token, its record is used instead of failing.
func (s *Service) Token(ctx context.Context, sub Subject, d Descriptor) (Token, error) {
	if err := s.validSubject(sub, &d); err != nil {
		return Token{}, err
	}
	if d.Kind == KindStatic {
		return Token{}, nil
	}
	key := sub.key()
	if t, ok := s.cache.get(key, s.now()); ok {
		return t, nil
	}
	return s.flight.do(ctx, cacheKey(key), func(fctx context.Context) (Token, error) {
		if t, ok := s.cache.get(key, s.now()); ok { // refreshed while we queued
			return t, nil
		}
		return s.refresh(fctx, sub, d)
	})
}

func (s *Service) refresh(ctx context.Context, sub Subject, d Descriptor) (Token, error) {
	p, err := s.reg.Resolve(d)
	if err != nil {
		return Token{}, err
	}
	key := sub.key()
	for round := 0; round < maxRefreshRounds; round++ {
		base, err := s.store.Get(ctx, key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Token{}, err // the store is broken: not a reason to ask the user to sign in again
		}
		saved := base.Secret
		res, perr := p.Begin(ctx, s.penv, Request{Descriptor: d, Subject: sub, Saved: saved})
		if perr != nil && !errors.Is(perr, ErrInvalidGrant) {
			return Token{}, scrub(perr, d, nil, saved)
		}

		if perr == nil && res.Token != nil {
			if res.Save != nil {
				switch err := s.persist(ctx, sub, d, base, res, false); {
				case err == nil:
				case errors.Is(err, ErrVersionConflict):
					// Another replica wrote meanwhile. Its record stays; our token is valid and is used.
					s.log.Debug("saved sign-in data was changed by another replica; keeping its version", "cluster", sub.ClusterID)
				default:
					// The token works now, but the rotated refresh token could not be saved: the next refresh
					// will ask the user to sign in again. Say so, but do not fail the request.
					s.log.Warn("the refreshed sign-in data could not be saved", "cluster", sub.ClusterID, "error", scrubText(err.Error()))
				}
			}
			s.cache.put(key, *res.Token)
			return *res.Token, nil
		}

		// The provider needs the user (a challenge) or rejected the refresh token. If a refresh token was
		// presented, another replica may have rotated it first: use its record instead of giving up.
		if len(saved) > 0 && s.savedChanged(ctx, key, base.Version) {
			continue
		}
		if perr == nil && res.Save != nil && len(res.Save) == 0 {
			if err := s.store.Delete(ctx, key); err != nil {
				return Token{}, err
			}
		}
		return Token{}, ErrChallengeRequired
	}
	return Token{}, ErrChallengeRequired
}

// savedChanged reports whether the stored record has a version other than base. A rejected refresh token
// can be the result of another replica rotating it a moment ago, whose write may not have landed yet, so the
// check is repeated after short pauses.
func (s *Service) savedChanged(ctx context.Context, k Key, base int64) bool {
	check := func() bool {
		rec, err := s.store.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			return base != 0
		}
		return err == nil && rec.Version != base
	}
	if check() {
		return true
	}
	for _, pause := range s.recheck {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pause):
		}
		if check() {
			return true
		}
	}
	return false
}

// Invalidate implements Invalidator: the API server rejected the token (401), so the cached one is dropped and the
// next Token call obtains a new one. Saved sign-in data is not touched.
func (s *Service) Invalidate(k Key) {
	s.cache.remove(k)
}

// Revoke implements Broker: it removes saved sign-in data and pending requests matching the filter and
// forgets the cached tokens. Empty fields of the filter match anything.
func (s *Service) Revoke(ctx context.Context, f RevokeFilter) error {
	for _, r := range s.live.takeIf(func(r liveRequest) bool {
		return (f.ClusterID == "" || r.key.ClusterID == f.ClusterID) && (f.OwnerID == "" || r.key.OwnerID == f.OwnerID) &&
			(f.Scope == "" || r.key.Scope == f.Scope)
	}) {
		r.cancel()
	}
	s.cache.drop(func(k Key) bool {
		return (f.ClusterID == "" || k.ClusterID == f.ClusterID) && (f.OwnerID == "" || k.OwnerID == f.OwnerID) &&
			(f.Scope == "" || k.Scope == f.Scope)
	}, keyFromCache)
	if _, err := s.chal.DeleteBy(ctx, f); err != nil {
		return err
	}
	_, err := s.store.Revoke(ctx, f)
	return err
}

// List implements Broker: the sign-ins an owner has saved (no secrets).
func (s *Service) List(ctx context.Context, owner string) ([]ConnectionInfo, error) {
	metas, err := s.store.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionInfo, 0, len(metas))
	for _, m := range metas {
		out = append(out, ConnectionInfo{ClusterID: m.ClusterID, Kind: m.Kind, Scope: m.Scope, Principal: m.Principal, ExpiresAt: m.ExpiresAt})
	}
	return out, nil
}
